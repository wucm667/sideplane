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
	jobsCreated           map[string]int64
	jobsCompleted         map[string]int64
	jobsFailed            map[string]int64
	configApplyRolledBack int64
}

// NewMetrics returns an initialized Metrics registry.
func NewMetrics() *Metrics {
	return &Metrics{
		jobsCreated:   map[string]int64{},
		jobsCompleted: map[string]int64{},
		jobsFailed:    map[string]int64{},
	}
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
	created := snapshotCounter(m.jobsCreated)
	completed := snapshotCounter(m.jobsCompleted)
	failed := snapshotCounter(m.jobsFailed)
	rolledBack := m.configApplyRolledBack
	m.mu.Unlock()

	writeCounter(w, "sideplane_jobs_created_total", "Jobs created by type.", created)
	writeCounter(w, "sideplane_jobs_completed_total", "Jobs completed by type.", completed)
	writeCounter(w, "sideplane_jobs_failed_total", "Jobs failed by type.", failed)

	fmt.Fprintln(w, "# HELP sideplane_config_apply_rolled_back_total Config applies that rolled back to backup.")
	fmt.Fprintln(w, "# TYPE sideplane_config_apply_rolled_back_total counter")
	fmt.Fprintf(w, "sideplane_config_apply_rolled_back_total %d\n", rolledBack)
}

type counterSample struct {
	label string
	value int64
}

func snapshotCounter(values map[string]int64) []counterSample {
	out := make([]counterSample, 0, len(values))
	for label, value := range values {
		out = append(out, counterSample{label: label, value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

func writeCounter(w http.ResponseWriter, name, help string, samples []counterSample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	if len(samples) == 0 {
		// Emit a zero series so scrapers always see the metric.
		fmt.Fprintf(w, "%s{type=\"none\"} 0\n", name)
		return
	}
	for _, sample := range samples {
		fmt.Fprintf(w, "%s{type=%q} %d\n", name, sample.label, sample.value)
	}
}
