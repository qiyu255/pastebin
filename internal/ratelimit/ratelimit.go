// Package ratelimit provides fixed-window rate limiting based on client IP.
package ratelimit

import (
	"sync"
	"time"

	"pastebin/internal/model"
)

// window tracks rate limit counters for a single time window.
type window struct {
	start   int64 // window start time (unix milliseconds)
	count   int64 // number of requests
	bytes   int64 // total bytes transferred
	usage   int64 // cumulative paste size created (delta)
}

// ipState holds read and write windows for a single IP.
type ipState struct {
	readWindows  []window
	writeWindows []window
}

// Limiter implements fixed-window rate limiting for read and write operations.
type Limiter struct {
	mu         sync.Mutex
	states     map[string]*ipState // IP -> state
	readCfg    model.RateLimitModeConfig
	writeCfg   model.RateLimitModeConfig
	stopCh     chan struct{}
	stopOnce   sync.Once
}

// New creates a new Limiter with the given configuration.
func New(readCfg, writeCfg model.RateLimitModeConfig) *Limiter {
	l := &Limiter{
		states:   make(map[string]*ipState),
		readCfg:  readCfg,
		writeCfg: writeCfg,
		stopCh:   make(chan struct{}),
	}

	// Start background cleanup goroutine (every 30 seconds)
	go l.cleanupLoop()

	return l
}

// Allow checks whether the request should be allowed.
// isRead indicates read (true) or write (false) operation.
// bytes is the request/response body size in bytes.
// usage is the net change in paste storage size (positive for new pastes, negative for deletions).
// Returns true if the request is allowed.
func (l *Limiter) Allow(ip string, isRead bool, bytes int64, usage int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UnixMilli()
	state, ok := l.states[ip]
	if !ok {
		state = &ipState{
			readWindows:  l.initWindows(l.readCfg.Windows, now),
			writeWindows: l.initWindows(l.writeCfg.Windows, now),
		}
		l.states[ip] = state
	}

	var windows []window
	var configs []model.WindowConfig
	if isRead {
		windows = state.readWindows
		configs = l.readCfg.Windows
	} else {
		windows = state.writeWindows
		configs = l.writeCfg.Windows
	}

	// Check each window
	for i, w := range windows {
		cfg := configs[i]
		durMs := cfg.Duration.Milliseconds()

		// Reset window if expired
		if now-w.start >= durMs {
			w.start = now
			w.count = 0
			w.bytes = 0
			w.usage = 0
		}

		// Check limits (0 means unlimited)
		if cfg.MaxRequests > 0 && w.count+1 > cfg.MaxRequests {
			return false
		}
		if cfg.MaxBytes > 0 && w.bytes+bytes > cfg.MaxBytes {
			return false
		}
		if cfg.MaxUsage > 0 && w.usage+usage > cfg.MaxUsage {
			return false
		}
	}

	// All windows pass, update counters
	for i := range windows {
		windows[i].count++
		windows[i].bytes += bytes
		windows[i].usage += usage
	}

	// Write back (need to handle the case where state struct has read/write separately)
	if isRead {
		state.readWindows = windows
	} else {
		state.writeWindows = windows
	}

	return true
}

// Stop terminates the background cleanup goroutine.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopCh)
	})
}

// initWindows creates initialized window slices from configuration.
func (l *Limiter) initWindows(cfgs []model.WindowConfig, now int64) []window {
	windows := make([]window, len(cfgs))
	for i := range cfgs {
		windows[i] = window{start: now}
	}
	return windows
}

// cleanupLoop periodically removes expired IP states.
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.cleanup()
		case <-l.stopCh:
			return
		}
	}
}

// cleanup removes IP states that haven't been used recently.
func (l *Limiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UnixMilli()

	// Find the longest window duration across all configs
	maxWindowMs := int64(0)
	for _, w := range l.readCfg.Windows {
		if ms := w.Duration.Milliseconds(); ms > maxWindowMs {
			maxWindowMs = ms
		}
	}
	for _, w := range l.writeCfg.Windows {
		if ms := w.Duration.Milliseconds(); ms > maxWindowMs {
			maxWindowMs = ms
		}
	}

	// Remove IPs whose windows are all stale (older than 2x max window + 60s)
	threshold := now - (2*maxWindowMs + 60000)

	for ip, state := range l.states {
		allStale := true
		for _, w := range state.readWindows {
			if w.start > threshold {
				allStale = false
				break
			}
		}
		if allStale {
			for _, w := range state.writeWindows {
				if w.start > threshold {
					allStale = false
					break
				}
			}
		}
		if allStale {
			delete(l.states, ip)
		}
	}
}
