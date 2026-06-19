package server

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Metrics holds Prometheus-compatible counters for job and config-apply
// outcomes. It exposes no secret values.
type Metrics struct {
	mu                    sync.Mutex
	heartbeats            map[string]int64
	jobsCreated           map[string]int64
	sidecarJobClaims      map[string]int64
	jobsCompleted         map[string]int64
	jobsFailed            map[string]int64
	lateJobResults        map[labelPair]int64
	configApplyRolledBack int64
}

// NewMetrics returns an initialized Metrics registry.
func NewMetrics() *Metrics {
	return &Metrics{
		heartbeats:       map[string]int64{},
		jobsCreated:      map[string]int64{},
		sidecarJobClaims: map[string]int64{},
		jobsCompleted:    map[string]int64{},
		jobsFailed:       map[string]int64{},
		lateJobResults:   map[labelPair]int64{},
	}
}

// IncHeartbeat records whether a heartbeat request was accepted or rejected.
func (m *Metrics) IncHeartbeat(status string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.heartbeats[status]++
	m.mu.Unlock()
}

// IncJobCreated records a created job of the given type.
func (m *Metrics) IncJobCreated(jobType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.jobsCreated[jobType]++
	m.mu.Unlock()
}

// IncSidecarJobClaim records a sidecar claiming a job of the given type.
func (m *Metrics) IncSidecarJobClaim(jobType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.sidecarJobClaims[jobType]++
	m.mu.Unlock()
}

// IncJobCompleted records a completed job of the given type.
func (m *Metrics) IncJobCompleted(jobType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.jobsCompleted[jobType]++
	m.mu.Unlock()
}

// IncJobFailed records a failed job of the given type.
func (m *Metrics) IncJobFailed(jobType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.jobsFailed[jobType]++
	m.mu.Unlock()
}

// IncLateJobResult records a sidecar result received after the server timed out the job.
func (m *Metrics) IncLateJobResult(jobType string, status string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lateJobResults[labelPair{left: jobType, right: status}]++
	m.mu.Unlock()
}

// IncConfigApplyRolledBack records a config apply that rolled back to backup.
func (m *Metrics) IncConfigApplyRolledBack() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.configApplyRolledBack++
	m.mu.Unlock()
}

// WriteProm renders the metrics in Prometheus text exposition format.
func (m *Metrics) WriteProm(w http.ResponseWriter) {
	m.mu.Lock()
	heartbeats := snapshotCounter(m.heartbeats)
	created := snapshotCounter(m.jobsCreated)
	claims := snapshotCounter(m.sidecarJobClaims)
	completed := snapshotCounter(m.jobsCompleted)
	failed := snapshotCounter(m.jobsFailed)
	lateResults := snapshotPairCounter(m.lateJobResults)
	rolledBack := m.configApplyRolledBack
	m.mu.Unlock()

	writeCounterWithLabel(w, "sideplane_heartbeats_total", "Heartbeats accepted or rejected by status.", "status", heartbeats)
	writeCounter(w, "sideplane_jobs_created_total", "Jobs created by type.", created)
	writeCounter(w, "sideplane_sidecar_job_claims_total", "Jobs claimed by sidecars by type.", claims)
	writeCounter(w, "sideplane_jobs_completed_total", "Jobs completed by type.", completed)
	writeCounter(w, "sideplane_jobs_failed_total", "Jobs failed by type.", failed)
	writePairCounter(w, "sideplane_job_late_results_total", "Sidecar job results received after server-side timeout.", "type", "status", lateResults)

	fmt.Fprintln(w, "# HELP sideplane_config_apply_rolled_back_total Config applies that rolled back to backup.")
	fmt.Fprintln(w, "# TYPE sideplane_config_apply_rolled_back_total counter")
	fmt.Fprintf(w, "sideplane_config_apply_rolled_back_total %d\n", rolledBack)
}

type counterSample struct {
	label string
	value int64
}

type pairCounterSample struct {
	left  string
	right string
	value int64
}

type labelPair struct {
	left  string
	right string
}

func snapshotCounter(values map[string]int64) []counterSample {
	out := make([]counterSample, 0, len(values))
	for label, value := range values {
		out = append(out, counterSample{label: label, value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

func snapshotPairCounter(values map[labelPair]int64) []pairCounterSample {
	out := make([]pairCounterSample, 0, len(values))
	for labels, value := range values {
		out = append(out, pairCounterSample{left: labels.left, right: labels.right, value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].left == out[j].left {
			return out[i].right < out[j].right
		}
		return out[i].left < out[j].left
	})
	return out
}

func writeCounter(w http.ResponseWriter, name, help string, samples []counterSample) {
	writeCounterWithLabel(w, name, help, "type", samples)
}

func writeCounterWithLabel(w http.ResponseWriter, name, help, labelName string, samples []counterSample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	if len(samples) == 0 {
		// Emit a zero series so scrapers always see the metric.
		fmt.Fprintf(w, "%s{%s=\"none\"} 0\n", name, labelName)
		return
	}
	for _, sample := range samples {
		fmt.Fprintf(w, "%s{%s=%q} %d\n", name, labelName, sample.label, sample.value)
	}
}

func writePairCounter(w http.ResponseWriter, name, help, leftLabelName, rightLabelName string, samples []pairCounterSample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	if len(samples) == 0 {
		fmt.Fprintf(w, "%s{%s=\"none\",%s=\"none\"} 0\n", name, leftLabelName, rightLabelName)
		return
	}
	for _, sample := range samples {
		fmt.Fprintf(w, "%s{%s=%q,%s=%q} %d\n", name, leftLabelName, sample.left, rightLabelName, sample.right, sample.value)
	}
}
