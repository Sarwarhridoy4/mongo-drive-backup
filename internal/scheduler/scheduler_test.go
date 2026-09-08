package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

func TestRunOnce_Success(t *testing.T) {
	var mu sync.Mutex
	called := false

	log := logger.New("test", nil)
	sched, err := New("0 * * * *", "UTC", func(ctx context.Context) error {
		mu.Lock()
		called = true
		mu.Unlock()
		return nil
	}, log)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	ctx := context.Background()
	if err := sched.RunOnce(ctx); err != nil {
		t.Fatalf("run once failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Errorf("job was not called")
	}
}

func TestRunOnce_Failure(t *testing.T) {
	expectedErr := errors.New("backup error")

	log := logger.New("test", nil)
	sched, err := New("0 * * * *", "UTC", func(ctx context.Context) error {
		return expectedErr
	}, log)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	ctx := context.Background()
	err = sched.RunOnce(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "backup error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunOnce_ConcurrentBlocks(t *testing.T) {
	started := make(chan bool)
	proceed := make(chan bool)

	jobCalled := 0

	log := logger.New("test", nil)
	sched, err := New("0 * * * *", "UTC", func(ctx context.Context) error {
		jobCalled++
		started <- true
		<-proceed
		return nil
	}, log)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	ctx := context.Background()

	go func() {
		_ = sched.RunOnce(ctx)
	}()

	<-started

	err = sched.RunOnce(ctx)
	if err == nil {
		t.Fatalf("expected error when job is running")
	}

	close(proceed)

	time.Sleep(100 * time.Millisecond)
	if jobCalled != 1 {
		t.Errorf("expected job to be called exactly once, got %d", jobCalled)
	}
}

func TestNew_InvalidTimezone(t *testing.T) {
	_, err := New("0 * * * *", "Invalid/Timezone", func(ctx context.Context) error {
		return nil
	}, nil)
	if err == nil {
		t.Errorf("expected error for invalid timezone")
	}
}
