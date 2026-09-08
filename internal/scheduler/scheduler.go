package scheduler

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sarwar/mongo-drive-backup/internal/logger"

	"github.com/robfig/cron/v3"
)

type Job func(ctx context.Context) error

type Scheduler struct {
	schedule string
	timezone string
	job      Job
	log      *logger.Logger
	cron     *cron.Cron
	mu       sync.Mutex
	running  bool
}

func New(schedule, timezone string, job Job, log *logger.Logger) (*Scheduler, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone: %w", err)
	}

	c := cron.New(cron.WithLocation(loc))
	return &Scheduler{
		schedule: schedule,
		timezone: timezone,
		job:      job,
		log:      log,
		cron:     c,
	}, nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	_, err := s.cron.AddJob(s.schedule, cron.FuncJob(func() {
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			s.log.Warn("backup already running; skipping scheduled execution", nil)
			return
		}
		s.running = true
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()

		if err := s.job(ctx); err != nil {
			s.log.Error("backup_failed", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}))
	if err != nil {
		return fmt.Errorf("add cron job: %w", err)
	}

	s.cron.Start()
	s.log.Info("scheduler_started", map[string]interface{}{
		"schedule": s.schedule,
		"timezone": s.timezone,
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-ctx.Done()

	s.cron.Stop()
	signal.Stop(sigCh)
	close(sigCh)

	s.log.Info("service_shutdown", nil)

	return nil
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("backup already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	return s.job(ctx)
}
