// Package cleanup periodically removes expired pastes from the store.
package cleanup

import (
	"log/slog"
	"math/rand"
	"time"

	"pastebin/internal/model"
	"pastebin/internal/store"
)

// Scheduler runs periodic expired paste cleanup with jitter.
type Scheduler struct {
	store  *store.Store
	timer  model.ParsedTimer
	logger *slog.Logger
	stopCh chan struct{}
}

// New creates a new cleanup Scheduler.
func New(s *store.Store, timer model.ParsedTimer, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		store:  s,
		timer:  timer,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins the periodic cleanup loop in a background goroutine.
func (s *Scheduler) Start() {
	if s.timer.Base.Duration == 0 {
		return
	}

	go s.loop()
}

// Stop terminates the cleanup loop.
func (s *Scheduler) Stop() {
	close(s.stopCh)
}

// loop runs the periodic cleanup.
func (s *Scheduler) loop() {
	// Do an initial cleanup shortly after startup
	time.Sleep(5 * time.Second)
	s.run()

	for {
		d := s.nextDelay()
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
			s.run()
		case <-s.stopCh:
			timer.Stop()
			return
		}
	}
}

// run executes a single cleanup pass.
func (s *Scheduler) run() {
	start := time.Now()
	n, err := s.store.DeleteExpired()
	if err != nil {
		s.logger.Error("cleanup failed", "error", err)
		return
	}
	elapsed := time.Since(start)
	if n > 0 {
		s.logger.Info("cleanup completed", "deleted", n, "elapsed", elapsed)
	}
}

// nextDelay computes the next interval with optional jitter.
func (s *Scheduler) nextDelay() time.Duration {
	d := s.timer.Base.Duration
	if s.timer.Jitter.Duration > 0 {
		jitter := time.Duration(rand.Int63n(int64(s.timer.Jitter.Duration)))
		d += jitter
	}
	return d
}
