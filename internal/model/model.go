// Package model defines the core data structures for the pastebin service.
package model

import "math"

// MaxExpiredAt is used for locked pastes to prevent expiration.
// Locked pastes have their expired_at set to this value.
const MaxExpiredAt = math.MaxInt64

// Paste represents a stored paste entry.
type Paste struct {
	ID        string
	Content   []byte
	Size      int64
	Locked    bool
	CreatedAt int64 // millisecond unix timestamp
	UpdatedAt int64 // millisecond unix timestamp
	ExpiredAt int64 // millisecond unix timestamp
	CreatedBy string // client IP
	UpdatedBy string // client IP
}

// Config holds all configuration for the pastebin service.
type Config struct {
	Listen              string            `json:"listen"`
	BehindProxy         bool              `json:"behind_proxy"`
	AdminKeyHash        string            `json:"admin_key_hash"`
	DBPath              string            `json:"db_path"`
	DBMaxSize           int64             `json:"db_max_size"`
	DBSizeCheckInterval string            `json:"db_size_check_interval"`
	CleanupInterval     string            `json:"cleanup_interval"`
	MaxPasteSize        int64             `json:"max_paste_size"`
	MultipartOverhead   int64             `json:"multipart_overhead"`
	IDMinLength         int               `json:"id_min_length"`
	IDMaxLength         int               `json:"id_max_length"`
	IDCharset           string            `json:"id_charset"`
	IDInitLength        int               `json:"id_init_length"`
	IDMaxCollisions     int               `json:"id_max_collisions"`
	TTLMap              map[string]string `json:"ttl_map"`
	StatsInterval       string            `json:"stats_interval"`
	RateLimit           RateLimitConfig   `json:"rate_limit"`
	LogLevel            string            `json:"log_level"`
	Log                 LogConfig         `json:"log"`
}

// ParsedTimer holds a parsed timer interval with optional jitter.
type ParsedTimer struct {
	Base   Duration // base interval
	Jitter Duration // max random offset (0 means no jitter)
}

// TTLRule maps an ID length range to a TTL duration.
type TTLRule struct {
	MinLen int
	MaxLen int      // -1 means unlimited (matches "N+" patterns)
	TTL    Duration
}

// RateLimitConfig holds read and write rate limit settings.
type RateLimitConfig struct {
	Read  RateLimitModeConfig `json:"read"`
	Write RateLimitModeConfig `json:"write"`
}

// RateLimitModeConfig holds rate limit windows for a single mode (read or write).
type RateLimitModeConfig struct {
	Windows []WindowConfig `json:"windows"`
}

// WindowConfig defines a single rate limit window.
type WindowConfig struct {
	Duration   Duration `json:"duration"`
	MaxRequests int64    `json:"max_requests"`
	MaxBytes    int64    `json:"max_bytes,omitempty"`
	MaxUsage    int64    `json:"max_usage,omitempty"`
}

// LogConfig holds log file rotation settings.
type LogConfig struct {
	Dir        string `json:"dir"`         // log directory, empty = stderr
	Rotation   string `json:"rotation"`    // "time" or "size"
	MaxAge     string `json:"max_age"`     // e.g. "7d"
	MaxSize    string `json:"max_size"`    // e.g. "100MB"
	MaxBackups int    `json:"max_backups"` // max old files for size rotation
}

// Stats holds a cached statistics report.
type Stats struct {
	DBSize       int64          `json:"db_size"`
	PasteCount   int64          `json:"paste_count"`
	RecentPastes []RecentPaste  `json:"recent_pastes"`
	MemAlloc     uint64         `json:"mem_alloc"`
	MemSys       uint64         `json:"mem_sys"`
	NumGC        uint32         `json:"num_gc"`
	GeneratedAt  int64          `json:"generated_at"`
}

// RecentPaste is a brief entry in the recent pastes list.
type RecentPaste struct {
	ID        string `json:"id"`
	UpdatedAt int64  `json:"updated_at"`
}
