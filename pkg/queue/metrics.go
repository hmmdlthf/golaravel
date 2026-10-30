package queue

import (
	"sync"
	"time"
)

// WorkerStatus captures runtime telemetry for a worker goroutine.
type WorkerStatus struct {
	ID        int       `json:"id"`
	Status    string    `json:"status"` // "idle", "busy", "stopped"
	Current   string    `json:"current_job,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Processed int64     `json:"processed"`
	Failed    int64     `json:"failed"`
}

// JobRecord represents a lightweight log of a recently processed job.
type JobRecord struct {
	ID        string        `json:"id"`
	Queue     string        `json:"queue"`
	Type      string        `json:"type"`
	Status    string        `json:"status"` // "success", "failed"
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
	Error     string        `json:"error,omitempty"`
}

// Metrics tracks queue throughput and worker states for the dashboard.
type Metrics struct {
	mu          sync.RWMutex
	processed   int64
	failed      int64
	workers     map[int]*WorkerStatus
	recentJobs  []JobRecord
	startedAt   time.Time
}

// NewMetrics creates a new Metrics tracker.
func NewMetrics() *Metrics {
	return &Metrics{
		workers:    make(map[int]*WorkerStatus),
		recentJobs: make([]JobRecord, 0, 50),
		startedAt:  time.Now(),
	}
}

// RegisterWorker adds a worker to the metrics monitor.
func (m *Metrics) RegisterWorker(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers[id] = &WorkerStatus{
		ID:        id,
		Status:    "idle",
		StartedAt: time.Now(),
	}
}

// SetWorkerBusy marks worker as actively executing a job.
func (m *Metrics) SetWorkerBusy(id int, jobType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.workers[id]; ok {
		w.Status = "busy"
		w.Current = jobType
	}
}

// RecordSuccess logs a successfully executed job.
func (m *Metrics) RecordSuccess(workerID int, payload *JobPayload, dur time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.processed++
	if w, ok := m.workers[workerID]; ok {
		w.Processed++
		w.Status = "idle"
		w.Current = ""
	}

	m.appendRecent(JobRecord{
		ID:        payload.ID,
		Queue:     payload.Queue,
		Type:      payload.Type,
		Status:    "success",
		Duration:  dur,
		Timestamp: time.Now(),
	})
}

// RecordFailure logs a failed job attempt.
func (m *Metrics) RecordFailure(workerID int, payload *JobPayload, dur time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failed++
	if w, ok := m.workers[workerID]; ok {
		w.Failed++
		w.Status = "idle"
		w.Current = ""
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	m.appendRecent(JobRecord{
		ID:        payload.ID,
		Queue:     payload.Queue,
		Type:      payload.Type,
		Status:    "failed",
		Duration:  dur,
		Timestamp: time.Now(),
		Error:     errStr,
	})
}

func (m *Metrics) appendRecent(rec JobRecord) {
	if len(m.recentJobs) >= 50 {
		m.recentJobs = m.recentJobs[1:]
	}
	m.recentJobs = append(m.recentJobs, rec)
}

// Snapshot returns a point-in-time metrics summary.
type Snapshot struct {
	TotalProcessed int64           `json:"total_processed"`
	TotalFailed    int64           `json:"total_failed"`
	ActiveWorkers  int             `json:"active_workers"`
	Workers        []WorkerStatus  `json:"workers"`
	RecentJobs     []JobRecord     `json:"recent_jobs"`
	Uptime         string          `json:"uptime"`
	UptimeSeconds  float64         `json:"uptime_seconds"`
}

// Snapshot creates a snapshot of the current metrics.
func (m *Metrics) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workersList := make([]WorkerStatus, 0, len(m.workers))
	active := 0
	for _, w := range m.workers {
		workersList = append(workersList, *w)
		if w.Status != "stopped" {
			active++
		}
	}

	recent := make([]JobRecord, len(m.recentJobs))
	copy(recent, m.recentJobs)

	uptime := time.Since(m.startedAt)

	return Snapshot{
		TotalProcessed: m.processed,
		TotalFailed:    m.failed,
		ActiveWorkers:  active,
		Workers:        workersList,
		RecentJobs:     recent,
		Uptime:         uptime.Round(time.Second).String(),
		UptimeSeconds:  uptime.Seconds(),
	}
}
