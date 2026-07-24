package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrConversationBusy = errors.New("conversation already has an active job")
	ErrQueueFull        = errors.New("user queue is full")
	ErrQueueTimeout     = errors.New("provider queue timeout")
)

type Scheduler struct {
	mu             sync.Mutex
	maxGlobal      int
	maxPerUser     int
	maxQueued      int
	queueTimeout   time.Duration
	runningGlobal  int
	runningByUser  map[string]int
	reservedByConv map[string]struct{}
	queues         map[string][]*waiter
	userOrder      []string
	nextUser       int
}

type waiter struct {
	userID         string
	conversationID string
	ready          chan struct{}
	cancelled      bool
}

type Lease struct {
	scheduler      *Scheduler
	userID         string
	conversationID string
	once           sync.Once
}

func NewScheduler(maxGlobal, maxPerUser, maxQueued int, queueTimeout time.Duration) *Scheduler {
	return &Scheduler{
		maxGlobal: maxGlobal, maxPerUser: maxPerUser, maxQueued: maxQueued, queueTimeout: queueTimeout,
		runningByUser: make(map[string]int), reservedByConv: make(map[string]struct{}),
		queues: make(map[string][]*waiter),
	}
}

func (s *Scheduler) Acquire(ctx context.Context, userID, conversationID string) (*Lease, error) {
	return s.AcquireWithQueueCallback(ctx, userID, conversationID, nil)
}

func (s *Scheduler) AcquireWithQueueCallback(
	ctx context.Context,
	userID string,
	conversationID string,
	onQueued func(position int) error,
) (*Lease, error) {
	s.mu.Lock()
	if _, exists := s.reservedByConv[conversationID]; exists {
		s.mu.Unlock()
		return nil, ErrConversationBusy
	}
	s.reservedByConv[conversationID] = struct{}{}

	if s.canRunLocked(userID) {
		s.runningGlobal++
		s.runningByUser[userID]++
		s.mu.Unlock()
		return &Lease{scheduler: s, userID: userID, conversationID: conversationID}, nil
	}
	if len(s.queues[userID]) >= s.maxQueued {
		delete(s.reservedByConv, conversationID)
		s.mu.Unlock()
		return nil, ErrQueueFull
	}
	entry := &waiter{userID: userID, conversationID: conversationID, ready: make(chan struct{})}
	if len(s.queues[userID]) == 0 {
		s.userOrder = append(s.userOrder, userID)
	}
	s.queues[userID] = append(s.queues[userID], entry)
	position := len(s.queues[userID])
	s.mu.Unlock()
	if onQueued != nil {
		if err := onQueued(position); err != nil {
			s.cancelWaiter(entry)
			return nil, err
		}
	}

	timer := time.NewTimer(s.queueTimeout)
	defer timer.Stop()
	select {
	case <-entry.ready:
		return &Lease{scheduler: s, userID: userID, conversationID: conversationID}, nil
	case <-ctx.Done():
		s.cancelWaiter(entry)
		return nil, ctx.Err()
	case <-timer.C:
		s.cancelWaiter(entry)
		return nil, ErrQueueTimeout
	}
}

func (s *Scheduler) Busy(conversationID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.reservedByConv[conversationID]
	return exists
}

func (lease *Lease) Release() {
	if lease == nil || lease.scheduler == nil {
		return
	}
	lease.once.Do(func() {
		lease.scheduler.release(lease.userID, lease.conversationID)
	})
}

func (s *Scheduler) release(userID, conversationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runningGlobal > 0 {
		s.runningGlobal--
	}
	if s.runningByUser[userID] > 0 {
		s.runningByUser[userID]--
	}
	delete(s.reservedByConv, conversationID)
	s.dispatchLocked()
}

func (s *Scheduler) cancelWaiter(target *waiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for userID, queue := range s.queues {
		for index, entry := range queue {
			if entry != target {
				continue
			}
			entry.cancelled = true
			s.queues[userID] = append(queue[:index], queue[index+1:]...)
			delete(s.reservedByConv, entry.conversationID)
			s.removeEmptyUserLocked(userID)
			s.dispatchLocked()
			return
		}
	}
	// If dispatch already selected the waiter, ready is closed and it owns a
	// running slot. The select will observe ready or the caller's context; the
	// latter case releases that slot here.
	select {
	case <-target.ready:
		if s.runningGlobal > 0 {
			s.runningGlobal--
		}
		if s.runningByUser[target.userID] > 0 {
			s.runningByUser[target.userID]--
		}
		delete(s.reservedByConv, target.conversationID)
		s.dispatchLocked()
	default:
	}
}

func (s *Scheduler) canRunLocked(userID string) bool {
	return s.runningGlobal < s.maxGlobal && s.runningByUser[userID] < s.maxPerUser
}

func (s *Scheduler) dispatchLocked() {
	for s.runningGlobal < s.maxGlobal && len(s.userOrder) > 0 {
		dispatched := false
		userCount := len(s.userOrder)
		for attempts := 0; attempts < userCount; attempts++ {
			if len(s.userOrder) == 0 {
				break
			}
			if s.nextUser >= len(s.userOrder) {
				s.nextUser = 0
			}
			userID := s.userOrder[s.nextUser]
			s.nextUser = (s.nextUser + 1) % len(s.userOrder)
			queue := s.queues[userID]
			if len(queue) == 0 {
				s.removeEmptyUserLocked(userID)
				continue
			}
			if !s.canRunLocked(userID) {
				continue
			}
			entry := queue[0]
			s.queues[userID] = queue[1:]
			s.removeEmptyUserLocked(userID)
			s.runningGlobal++
			s.runningByUser[userID]++
			close(entry.ready)
			dispatched = true
			break
		}
		if !dispatched {
			return
		}
	}
}

func (s *Scheduler) removeEmptyUserLocked(userID string) {
	if len(s.queues[userID]) != 0 {
		return
	}
	delete(s.queues, userID)
	for index, value := range s.userOrder {
		if value != userID {
			continue
		}
		s.userOrder = append(s.userOrder[:index], s.userOrder[index+1:]...)
		if index < s.nextUser {
			s.nextUser--
		}
		if s.nextUser < 0 || s.nextUser >= len(s.userOrder) {
			s.nextUser = 0
		}
		return
	}
}
