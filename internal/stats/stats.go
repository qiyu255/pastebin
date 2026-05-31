// Package stats generates and caches server statistics reports.
package stats

import (
	"log/slog"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"pastebin/internal/model"
	"pastebin/internal/store"
)

// Reporter periodically generates and caches statistics.
type Reporter struct {
	store  *store.Store
	timer  model.ParsedTimer
	logger *slog.Logger

	mu     sync.RWMutex
	cached *model.Stats

	stopCh chan struct{}
}

// New creates a new statistics Reporter.
func New(s *store.Store, timer model.ParsedTimer, logger *slog.Logger) *Reporter {
	return &Reporter{
		store:  s,
		timer:  timer,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins periodic statistics generation in a background goroutine.
func (r *Reporter) Start() {
	if r.timer.Base.Duration == 0 {
		return
	}

	// Generate initial stats
	r.generate()

	go r.loop()
}

// Stop terminates the statistics loop.
func (r *Reporter) Stop() {
	close(r.stopCh)
}

// GetCached returns the most recently cached statistics report.
// May return nil if no report has been generated yet.
func (r *Reporter) GetCached() *model.Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cached
}

// GenerateNow generates a fresh report, caches it, and returns it.
func (r *Reporter) GenerateNow() *model.Stats {
	return r.generate()
}

// loop runs the periodic statistics generation.
func (r *Reporter) loop() {
	for {
		d := r.nextDelay()
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
			r.generate()
		case <-r.stopCh:
			timer.Stop()
			return
		}
	}
}

// generate creates a fresh stats report and caches it.
func (r *Reporter) generate() *model.Stats {
	start := time.Now()

	s := &model.Stats{
		GeneratedAt: time.Now().UnixMilli(),
	}

	// Database size
	if sz, err := r.store.DBSize(); err != nil {
		r.logger.Error("stats: db size", "error", err)
	} else {
		s.DBSize = sz
	}

	// Paste count
	if n, err := r.store.Count(); err != nil {
		r.logger.Error("stats: count", "error", err)
	} else {
		s.PasteCount = n
	}

	// Recent pastes (10 most recent)
	if recent, err := r.store.RecentPastes(10); err != nil {
		r.logger.Error("stats: recent pastes", "error", err)
	} else {
		if recent == nil {
			recent = []model.RecentPaste{}
		}
		s.RecentPastes = recent
	}

	// Memory stats
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.MemAlloc = mem.Alloc
	s.MemSys = mem.Sys
	s.NumGC = mem.NumGC

	r.mu.Lock()
	r.cached = s
	r.mu.Unlock()

	elapsed := time.Since(start)
	r.logger.Debug("stats generated", "elapsed", elapsed)

	return s
}

// nextDelay computes the next interval with optional jitter.
func (r *Reporter) nextDelay() time.Duration {
	d := r.timer.Base.Duration
	if r.timer.Jitter.Duration > 0 {
		jitter := time.Duration(rand.Int63n(int64(r.timer.Jitter.Duration)))
		d += jitter
	}
	return d
}
