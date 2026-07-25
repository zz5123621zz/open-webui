package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResponseJobIgnoresViewerDisconnectAndHonorsExplicitCancel(t *testing.T) {
	lifecycle, stopLifecycle := context.WithCancelCause(context.Background())
	server := &Server{
		responseContext: lifecycle,
		responseCancel:  stopLifecycle,
	}
	viewerContext, disconnectViewer := context.WithCancel(context.Background())
	jobContext, cancelJob, finishJob, err := server.beginResponseJob(viewerContext)
	if err != nil {
		t.Fatal(err)
	}
	defer finishJob()

	disconnectViewer()
	select {
	case <-jobContext.Done():
		t.Fatalf("viewer disconnect cancelled response job: %v", context.Cause(jobContext))
	case <-time.After(20 * time.Millisecond):
	}

	cancelJob(errResponseCancelled)
	select {
	case <-jobContext.Done():
	case <-time.After(time.Second):
		t.Fatal("explicit cancellation did not stop the response job")
	}
	if !errors.Is(context.Cause(jobContext), errResponseCancelled) {
		t.Fatalf("response cancellation cause = %v", context.Cause(jobContext))
	}
}

func TestResponseShutdownCancelsJobsAndRejectsNewWork(t *testing.T) {
	lifecycle, stopLifecycle := context.WithCancelCause(context.Background())
	server := &Server{
		responseContext: lifecycle,
		responseCancel:  stopLifecycle,
	}
	jobContext, _, finishJob, err := server.beginResponseJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jobFinished := make(chan struct{})
	go func() {
		<-jobContext.Done()
		finishJob()
		close(jobFinished)
	}()

	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(), time.Second,
	)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-jobFinished:
	case <-time.After(time.Second):
		t.Fatal("response job did not finish after shutdown")
	}
	if !errors.Is(context.Cause(jobContext), errServiceInterrupted) {
		t.Fatalf("shutdown cause = %v", context.Cause(jobContext))
	}
	if _, _, _, err := server.beginResponseJob(context.Background()); !errors.Is(
		err, errServiceStopping,
	) {
		t.Fatalf("new response after shutdown error = %v", err)
	}
}
