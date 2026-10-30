package queue

import (
	"sync"
	"time"
)

// MemoryDriver provides an in-memory queue driver with delayed job support.
type MemoryDriver struct {
	mu          sync.Mutex
	queues      map[string][]*JobPayload
	delayed     map[string][]*JobPayload
	notifyChans map[string]chan struct{}
	stopChan    chan struct{}
}

// NewMemoryDriver creates an in-memory queue driver.
func NewMemoryDriver() *MemoryDriver {
	d := &MemoryDriver{
		queues:      make(map[string][]*JobPayload),
		delayed:     make(map[string][]*JobPayload),
		notifyChans: make(map[string]chan struct{}),
		stopChan:    make(chan struct{}),
	}
	go d.processDelayed()
	return d
}

func (d *MemoryDriver) getNotifyChan(queue string) chan struct{} {
	ch, ok := d.notifyChans[queue]
	if !ok {
		ch = make(chan struct{}, 1)
		d.notifyChans[queue] = ch
	}
	return ch
}

func (d *MemoryDriver) notify(queue string) {
	ch := d.getNotifyChan(queue)
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Push enqueues a job immediately.
func (d *MemoryDriver) Push(queue string, payload *JobPayload) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	payload.Queue = queue
	payload.AvailableAt = time.Now()
	d.queues[queue] = append(d.queues[queue], payload)
	d.notify(queue)
	return nil
}

// Later enqueues a job with delay.
func (d *MemoryDriver) Later(queue string, delay time.Duration, payload *JobPayload) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	payload.Queue = queue
	payload.AvailableAt = time.Now().Add(delay)
	d.delayed[queue] = append(d.delayed[queue], payload)
	return nil
}

// Pop pops the next ready job. If empty, waits up to timeout.
func (d *MemoryDriver) Pop(queue string, timeout time.Duration) (*JobPayload, error) {
	start := time.Now()

	for {
		d.mu.Lock()
		items := d.queues[queue]
		if len(items) > 0 {
			item := items[0]
			d.queues[queue] = items[1:]
			d.mu.Unlock()
			return item, nil
		}
		ch := d.getNotifyChan(queue)
		d.mu.Unlock()

		if timeout <= 0 {
			return nil, ErrQueueEmpty
		}

		elapsed := time.Since(start)
		remaining := timeout - elapsed
		if remaining <= 0 {
			return nil, ErrQueueEmpty
		}

		select {
		case <-ch:
			// Notified of new job, re-check loop
		case <-time.After(remaining):
			return nil, ErrQueueEmpty
		case <-d.stopChan:
			return nil, ErrQueueEmpty
		}
	}
}

// Size returns the count of ready and delayed jobs in queue.
func (d *MemoryDriver) Size(queue string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return int64(len(d.queues[queue]) + len(d.delayed[queue])), nil
}

// Clear clears all ready and delayed jobs for the queue.
func (d *MemoryDriver) Clear(queue string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.queues, queue)
	delete(d.delayed, queue)
	return nil
}

// Close stops the background delayed job processor.
func (d *MemoryDriver) Close() {
	select {
	case <-d.stopChan:
	default:
		close(d.stopChan)
	}
}

func (d *MemoryDriver) processDelayed() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case now := <-ticker.C:
			d.mu.Lock()
			for qName, items := range d.delayed {
				var remaining []*JobPayload
				for _, item := range items {
					if now.After(item.AvailableAt) || now.Equal(item.AvailableAt) {
						d.queues[qName] = append(d.queues[qName], item)
						d.notify(qName)
					} else {
						remaining = append(remaining, item)
					}
				}
				d.delayed[qName] = remaining
			}
			d.mu.Unlock()
		}
	}
}

// MemoryFailedJobStore stores failed jobs in memory.
type MemoryFailedJobStore struct {
	mu   sync.RWMutex
	jobs []*FailedJob
}

// NewMemoryFailedJobStore creates an in-memory DLQ store.
func NewMemoryFailedJobStore() *MemoryFailedJobStore {
	return &MemoryFailedJobStore{
		jobs: make([]*FailedJob, 0),
	}
}

func (s *MemoryFailedJobStore) Log(job *FailedJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
	return nil
}

func (s *MemoryFailedJobStore) All(limit, offset int) ([]*FailedJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if offset >= len(s.jobs) {
		return []*FailedJob{}, nil
	}
	end := offset + limit
	if end > len(s.jobs) || limit <= 0 {
		end = len(s.jobs)
	}
	res := make([]*FailedJob, end-offset)
	copy(res, s.jobs[offset:end])
	return res, nil
}

func (s *MemoryFailedJobStore) Find(id string) (*FailedJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, j := range s.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, ErrJobNotFound
}

func (s *MemoryFailedJobStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			return nil
		}
	}
	return ErrJobNotFound
}

func (s *MemoryFailedJobStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = nil
	return nil
}

func (s *MemoryFailedJobStore) Count() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.jobs)), nil
}
