package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wucm667/sideplane/internal/rollout"
	"github.com/wucm667/sideplane/internal/store"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// RolloutOrchestratorConfig configures the server-side rollout scheduler.
type RolloutOrchestratorConfig struct {
	Store      store.Store
	Freshness  FreshnessPolicy
	SigningKey spcrypto.KeyPair
	Interval   time.Duration
	Logger     *slog.Logger
	Now        func() time.Time
}

// StartRolloutOrchestrator periodically reconciles non-terminal rollouts until ctx is done.
func StartRolloutOrchestrator(ctx context.Context, cfg RolloutOrchestratorConfig) {
	if cfg.Store == nil || cfg.Interval <= 0 {
		return
	}
	if cfg.Logger == nil {
		cfg.Logger = discardLogger()
	}
	orchestrator := NewRolloutOrchestrator(cfg)
	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := orchestrator.ReconcileOnce(ctx); err != nil {
					cfg.Logger.Warn("rollout reconcile failed", "error", err)
				}
			}
		}
	}()
}

// RolloutOrchestrator adapts the pure rollout engine to server dependencies.
type RolloutOrchestrator struct {
	store      store.Store
	freshness  FreshnessPolicy
	signingKey spcrypto.KeyPair
	logger     *slog.Logger
	now        func() time.Time
	engine     rollout.Engine
}

// NewRolloutOrchestrator returns a rollout reconciler that can be stepped in tests.
func NewRolloutOrchestrator(cfg RolloutOrchestratorConfig) *RolloutOrchestrator {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if cfg.Freshness.Now == nil {
		cfg.Freshness.Now = func() time.Time { return now().UTC() }
	}
	o := &RolloutOrchestrator{
		store:      cfg.Store,
		freshness:  cfg.Freshness,
		signingKey: cfg.SigningKey,
		logger:     cfg.Logger,
		now:        now,
	}
	if o.logger == nil {
		o.logger = discardLogger()
	}
	o.engine = rollout.Engine{Clock: o, Dispatcher: o, Health: o}
	return o
}

// Now implements rollout.Clock.
func (o *RolloutOrchestrator) Now() time.Time {
	return o.now().UTC()
}

// ReconcileOnce steps all currently non-terminal rollouts once.
func (o *RolloutOrchestrator) ReconcileOnce(ctx context.Context) error {
	list, err := o.store.ListRollouts(ctx, store.RolloutFilter{Limit: store.MaxRolloutListLimit})
	if err != nil {
		return err
	}
	for _, current := range list.Rollouts {
		if rolloutTerminal(current.State) {
			continue
		}
		next, err := o.engine.Step(ctx, current)
		if err != nil {
			return err
		}
		if rolloutEqual(current, next) {
			continue
		}
		if err := o.store.UpdateRollout(ctx, next); err != nil {
			if errors.Is(err, store.ErrRolloutNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

// DispatchConfigApply implements rollout.DispatchConfigApply.
func (o *RolloutOrchestrator) DispatchConfigApply(ctx context.Context, ro protocol.Rollout, nodeID string) (string, error) {
	job, err := createSignedConfigApplyJob(ctx, o.store, o.signingKey, nodeID, ro.Spec.RuntimeType, ro.Spec.Profile, ro.Spec.Target, !ro.Spec.Live, o.Now())
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

// NodeHealth implements rollout.HealthReader.
func (o *RolloutOrchestrator) NodeHealth(ctx context.Context, ro protocol.Rollout, node protocol.RolloutNodeProgress) (rollout.NodeHealth, error) {
	health := rollout.NodeHealth{}
	nodes, err := o.store.ListNodes(ctx)
	if err != nil {
		return health, err
	}
	for i := range nodes {
		nodes[i].State = o.freshness.StateFor(nodes[i].LastHeartbeatAt)
		if nodes[i].NodeID == node.NodeID && nodes[i].State == protocol.NodeStateOffline {
			health.Offline = true
		}
	}
	job, err := o.store.GetJob(ctx, node.JobID)
	if err != nil {
		return health, err
	}
	if job == nil {
		return health, nil
	}
	switch job.Status {
	case protocol.JobStatusCompleted:
		health.ApplySucceeded = true
	case protocol.JobStatusFailed:
		health.ApplyFailed = true
		health.Error = job.Error
	}
	if ro.Spec.Live && health.ApplySucceeded {
		health.Drift = !nodeRuntimeMatchesTarget(nodes, node.NodeID, ro.Spec.RuntimeType, ro.Spec.Target)
	}
	return health, nil
}

func createSignedConfigApplyJob(ctx context.Context, dataStore store.Store, signingKey spcrypto.KeyPair, nodeID string, runtimeType string, profile string, target protocol.ProviderModelConfig, dryRun bool, now time.Time) (protocol.Job, error) {
	runtimeType = strings.TrimSpace(runtimeType)
	if runtimeType == "" {
		runtimeType = "hermes"
	}
	profile = strings.TrimSpace(profile)
	if len(signingKey.PrivateKey) == 0 {
		return protocol.Job{}, fmt.Errorf("server signing key is not configured")
	}
	exists, err := dataStore.NodeExists(ctx, nodeID)
	if err != nil {
		return protocol.Job{}, err
	}
	if !exists {
		return protocol.Job{}, store.ErrNodeNotFound
	}
	if strings.TrimSpace(target.Provider) == "" || strings.TrimSpace(target.Model) == "" {
		return protocol.Job{}, fmt.Errorf("desired provider and model must be set before applying config")
	}
	if err := spconfig.ValidateProviderModelSelection(target); err != nil {
		return protocol.Job{}, fmt.Errorf("invalid desired provider/model: %w", err)
	}
	actual, err := latestActualSnapshotFromStore(ctx, dataStore, nodeID, runtimeType, profile)
	if err != nil {
		return protocol.Job{}, err
	}
	if actual == nil || strings.TrimSpace(actual.ConfigPath) == "" {
		return protocol.Job{}, fmt.Errorf("no known config path for node; run a deep probe first")
	}
	planID, err := newPlanID()
	if err != nil {
		return protocol.Job{}, err
	}
	mode := protocol.ConfigPlanModeDryRun
	if !dryRun {
		mode = protocol.ConfigPlanModeLive
	}
	plan := protocol.ConfigPlan{
		ID:           planID,
		Schema:       protocol.ConfigPlanSchema,
		Version:      protocol.ConfigPlanVersion,
		CreatedAt:    now.UTC(),
		TargetNodeID: nodeID,
		Mode:         mode,
		Body: protocol.ConfigPlanBody{
			RuntimeType: runtimeType,
			Profile:     actual.ConfigPath,
			Desired:     target,
			DryRun:      dryRun,
		},
	}
	signed, err := protocol.SignConfigPlan(plan, signingKey.PrivateKey)
	if err != nil {
		return protocol.Job{}, err
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		return protocol.Job{}, err
	}
	return dataStore.CreateJob(ctx, protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: string(payload),
	}, nodeID, now.UTC())
}

func latestActualSnapshotFromStore(ctx context.Context, dataStore store.Store, nodeID string, runtimeType string, profile string) (*protocol.RuntimeConfigSnapshot, error) {
	jobs, err := dataStore.ListNodeJobs(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Type != protocol.JobTypeDeepProbe || job.Status != protocol.JobStatusCompleted || strings.TrimSpace(job.ResultJSON) == "" {
			continue
		}
		var result protocol.DeepProbeResult
		if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
			continue
		}
		for _, snapshot := range result.ConfigSnapshots {
			if runtimeType != "" && snapshot.RuntimeType != runtimeType {
				continue
			}
			if profile != "" && snapshot.Profile != profile {
				continue
			}
			matched := snapshot
			return &matched, nil
		}
	}
	return nil, nil
}

func nodeRuntimeMatchesTarget(nodes []protocol.NodeStatus, nodeID string, runtimeType string, target protocol.ProviderModelConfig) bool {
	for _, node := range nodes {
		if node.NodeID != nodeID {
			continue
		}
		for _, runtime := range node.Runtimes {
			if runtimeType != "" && runtime.Type != runtimeType {
				continue
			}
			if runtime.Provider == target.Provider && runtime.Model == target.Model {
				return true
			}
		}
	}
	return false
}

func rolloutTerminal(state protocol.RolloutState) bool {
	return state == protocol.RolloutStateCompleted || state == protocol.RolloutStateAborted || state == protocol.RolloutStateFailed
}

func rolloutEqual(a, b protocol.Rollout) bool {
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)
	return string(pa) == string(pb)
}
