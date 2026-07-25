package httpapi

import (
	"context"
	"errors"
	"sync"
	"time"
)

const maxResponseJobDuration = 30 * time.Minute

var (
	errResponseCancelled  = errors.New("response cancelled by user")
	errResponseTimeout    = errors.New("response exceeded its maximum duration")
	errServiceStopping    = errors.New("service is stopping")
	errServiceInterrupted = errors.New("response interrupted by service shutdown")
)

func (s *Server) beginResponseJob(
	clientContext context.Context,
) (context.Context, context.CancelCauseFunc, func(), error) {
	s.responseMu.Lock()
	if s.shuttingDown {
		s.responseMu.Unlock()
		return nil, nil, nil, errServiceStopping
	}
	s.responseWG.Add(1)
	lifecycle := s.responseContext
	s.responseMu.Unlock()

	timeoutContext, cancelTimeout := context.WithTimeoutCause(
		context.WithoutCancel(clientContext),
		maxResponseJobDuration,
		errResponseTimeout,
	)
	jobContext, cancelJob := context.WithCancelCause(timeoutContext)
	stopLifecycleLink := context.AfterFunc(lifecycle, func() {
		cancelJob(errServiceInterrupted)
	})

	var once sync.Once
	finish := func() {
		once.Do(func() {
			stopLifecycleLink()
			cancelJob(nil)
			cancelTimeout()
			s.responseWG.Done()
		})
	}
	return jobContext, cancelJob, finish, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.responseMu.Lock()
	if !s.shuttingDown {
		s.shuttingDown = true
		s.responseCancel(errServiceInterrupted)
	}
	s.responseMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.responseWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func responseInterruptionCode(ctx context.Context) string {
	switch {
	case errors.Is(context.Cause(ctx), errResponseCancelled):
		return "response_cancelled"
	case errors.Is(context.Cause(ctx), errServiceInterrupted):
		return "service_interrupted"
	case errors.Is(context.Cause(ctx), errResponseTimeout):
		return "response_timeout"
	default:
		return "response_interrupted"
	}
}
