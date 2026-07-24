package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerLimitsAndConversationExclusion(t *testing.T) {
	scheduler := NewScheduler(2, 1, 1, 100*time.Millisecond)
	first, err := scheduler.Acquire(context.Background(), "user-a", "conversation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := scheduler.Acquire(context.Background(), "user-a", "conversation-a"); !errors.Is(err, ErrConversationBusy) {
		t.Fatalf("same conversation error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := scheduler.Acquire(ctx, "user-a", "conversation-b"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued Acquire() error = %v", err)
	}
}

func TestSchedulerDispatchesQueuedJob(t *testing.T) {
	scheduler := NewScheduler(1, 1, 2, time.Second)
	first, err := scheduler.Acquire(context.Background(), "user-a", "conversation-a")
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan *Lease, 1)
	go func() {
		lease, _ := scheduler.Acquire(context.Background(), "user-b", "conversation-b")
		result <- lease
	}()
	time.Sleep(10 * time.Millisecond)
	first.Release()
	select {
	case second := <-result:
		if second == nil {
			t.Fatal("queued lease = nil")
		}
		second.Release()
	case <-time.After(time.Second):
		t.Fatal("queued lease was not dispatched")
	}
}

func TestSchedulerReportsQueuePosition(t *testing.T) {
	scheduler := NewScheduler(1, 1, 2, time.Second)
	first, err := scheduler.Acquire(context.Background(), "user-a", "conversation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	position := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, acquireErr := scheduler.AcquireWithQueueCallback(
			ctx, "user-b", "conversation-b", func(value int) error {
				position <- value
				return nil
			},
		)
		result <- acquireErr
	}()
	select {
	case got := <-position:
		if got != 1 {
			t.Fatalf("queue position = %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queue callback was not called")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireWithQueueCallback() error = %v", err)
	}
}
