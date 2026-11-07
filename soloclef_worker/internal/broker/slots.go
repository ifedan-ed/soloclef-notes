package broker

import "sync"

// slots is the concurrency the tier exists for: a limit per queue, and a
// separate limit per task name so one of the four heavy task classes cannot
// take the whole heavy queue — the thing `celery_heavy`'s single concurrency
// number could not express.
type slots struct {
	mu           sync.Mutex
	queueLimit   map[string]int
	taskLimit    map[string]int
	queueRunning map[string]int
	taskRunning  map[string]int
	slotWasFreed chan struct{}
}

func newSlots(queueLimit, taskLimit map[string]int) *slots {
	return &slots{
		queueLimit:   copyLimits(queueLimit),
		taskLimit:    copyLimits(taskLimit),
		queueRunning: map[string]int{},
		taskRunning:  map[string]int{},
		slotWasFreed: make(chan struct{}),
	}
}

// openQueues is the subset of queues with room for one more message. The
// consumer polls only these, which is how a full queue stops being read
// without stopping the others.
func (s *slots) openQueues(queues []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	open := make([]string, 0, len(queues))
	for _, queue := range queues {
		if s.queueRunning[queue] < s.queueLimit[queue] {
			open = append(open, queue)
		}
	}
	return open
}

// takeQueue claims a queue slot. Only the single polling goroutine calls it,
// so a slot found open by openQueues is still open here.
func (s *slots) takeQueue(queue string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queueRunning[queue] >= s.queueLimit[queue] {
		return false
	}
	s.queueRunning[queue]++
	return true
}

// takeTask claims a task class slot. A task with no limit of its own is always
// admitted; its queue's limit is what bounds it.
func (s *slots) takeTask(task string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit, limited := s.taskLimit[task]
	if limited && s.taskRunning[task] >= limit {
		return false
	}
	s.taskRunning[task]++
	return true
}

// release gives back a queue slot, a task slot, or both; an empty name skips
// that half, which is what a message rejected between the two needs.
func (s *slots) release(queue, task string) {
	s.mu.Lock()
	if queue != "" {
		decrement(s.queueRunning, queue)
	}
	if task != "" {
		decrement(s.taskRunning, task)
	}
	freed := s.slotWasFreed
	s.slotWasFreed = make(chan struct{})
	s.mu.Unlock()
	close(freed)
}

// freed is closed the next time any slot is released, so a consumer with every
// queue full waits instead of polling.
func (s *slots) freed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slotWasFreed
}

func (s *slots) inFlight() (byQueue, byTask map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyLimits(s.queueRunning), copyLimits(s.taskRunning)
}

func copyLimits(from map[string]int) map[string]int {
	to := make(map[string]int, len(from))
	for key, value := range from {
		to[key] = value
	}
	return to
}

func decrement(counts map[string]int, key string) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}
