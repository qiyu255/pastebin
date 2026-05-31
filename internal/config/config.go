// Package config loads and parses the pastebin configuration file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"pastebin/internal/model"
)

// Load reads and parses a JSON configuration file.
func Load(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg model.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// validate checks that required fields are set and applies sensible defaults.
func validate(c *model.Config) error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DBPath == "" {
		c.DBPath = "pastebin.db"
	}
	if c.DBMaxSize <= 0 {
		c.DBMaxSize = 1 << 30 // 1 GB
	}
	if c.MaxPasteSize <= 0 {
		c.MaxPasteSize = 10 << 20 // 10 MB
	}
	if c.MultipartOverhead <= 0 {
		c.MultipartOverhead = 4096
	}
	if c.IDMinLength < 1 {
		c.IDMinLength = 1
	}
	if c.IDMaxLength < c.IDMinLength {
		c.IDMaxLength = 16
	}
	if c.IDCharset == "" {
		c.IDCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}
	if c.IDInitLength < c.IDMinLength {
		c.IDInitLength = c.IDMinLength
	}
	if c.IDMaxCollisions <= 0 {
		c.IDMaxCollisions = 10
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Log.Rotation == "" {
		c.Log.Rotation = "time"
	}
	if c.Log.MaxAge == "" {
		c.Log.MaxAge = "7d"
	}
	if c.Log.MaxSize == "" {
		c.Log.MaxSize = "100MB"
	}
	if c.Log.MaxBackups <= 0 {
		c.Log.MaxBackups = 10
	}
	return nil
}

// ParseTimer parses a timer string of the form "base[+jitter]".
// Examples: "1m+20s" → base=1m, jitter max=20s; "5m" → base=5m, jitter=0.
func ParseTimer(s string) (model.ParsedTimer, error) {
	if s == "" {
		return model.ParsedTimer{}, nil
	}

	var pt model.ParsedTimer

	// Split on '+' to separate base and jitter
	parts := strings.SplitN(s, "+", 2)

	base, err := time.ParseDuration(strings.TrimSpace(parts[0]))
	if err != nil {
		return pt, fmt.Errorf("parse timer base %q: %w", parts[0], err)
	}
	pt.Base = model.Duration{Duration: base}

	if len(parts) == 2 {
		jitter, err := time.ParseDuration(strings.TrimSpace(parts[1]))
		if err != nil {
			return pt, fmt.Errorf("parse timer jitter %q: %w", parts[1], err)
		}
		pt.Jitter = model.Duration{Duration: jitter}
	}

	return pt, nil
}

// ParseTTLMap converts the raw TTL map from config into sorted TTL rules.
// Config keys: "1", "2-3", "4-8", "9+" with duration string values like "1h", "1d".
// Rules are sorted by MinLen ascending for efficient lookup.
func ParseTTLMap(raw map[string]string) ([]model.TTLRule, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("ttl_map is empty")
	}

	var rules []model.TTLRule

	for key, val := range raw {
		dur, err := ParseDurationWithDays(val)
		if err != nil {
			return nil, fmt.Errorf("ttl_map key %q: invalid duration %q: %w", key, val, err)
		}

		minLen, maxLen, err := parseTTLRange(key)
		if err != nil {
			return nil, fmt.Errorf("ttl_map key %q: %w", key, err)
		}

		rules = append(rules, model.TTLRule{
			MinLen: minLen,
			MaxLen: maxLen,
			TTL:    model.Duration{Duration: dur},
		})
	}

	// Sort by MinLen ascending
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].MinLen < rules[j].MinLen
	})

	return rules, nil
}

// parseTTLRange parses a range key like "1", "2-3", or "9+".
// Returns minLen, maxLen (-1 for unlimited), and error.
func parseTTLRange(key string) (int, int, error) {
	key = strings.TrimSpace(key)

	if strings.HasSuffix(key, "+") {
		// "9+" → minLen=9, maxLen=-1 (unlimited)
		n, err := strconv.Atoi(strings.TrimSuffix(key, "+"))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range %q", key)
		}
		return n, -1, nil
	}

	if strings.Contains(key, "-") {
		// "2-3" → minLen=2, maxLen=3
		parts := strings.SplitN(key, "-", 2)
		minLen, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range %q", key)
		}
		maxLen, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range %q", key)
		}
		if minLen > maxLen {
			return 0, 0, fmt.Errorf("invalid range %q: min > max", key)
		}
		return minLen, maxLen, nil
	}

	// Single number: "1" → minLen=1, maxLen=1
	n, err := strconv.Atoi(key)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range %q", key)
	}
	return n, n, nil
}

// ParseDurationWithDays parses a duration string that may include "d" for days.
// Go's time.ParseDuration doesn't support days, so we convert them to hours.
func ParseDurationWithDays(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)

	// Handle simple case: no days
	if !strings.Contains(s, "d") {
		return time.ParseDuration(s)
	}

	// Parse "Nd" prefix
	var days int
	var rest string
	for i, c := range s {
		if c == 'd' {
			daysStr := s[:i]
			var err error
			days, err = strconv.Atoi(daysStr)
			if err != nil {
				return 0, fmt.Errorf("invalid days in %q: %w", s, err)
			}
			rest = s[i+1:]
			break
		}
	}

	d := time.Duration(days) * 24 * time.Hour

	if rest != "" {
		restDur, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid duration after days in %q: %w", s, err)
		}
		d += restDur
	}

	return d, nil
}

// TTLForLength returns the TTL for a given ID length using the rules.
// Rules must be sorted by MinLen ascending.
func TTLForLength(rules []model.TTLRule, length int) time.Duration {
	for _, rule := range rules {
		if length >= rule.MinLen && (rule.MaxLen == -1 || length <= rule.MaxLen) {
			return rule.TTL.Duration
		}
	}
	// Fallback: use the last rule
	if len(rules) > 0 {
		return rules[len(rules)-1].TTL.Duration
	}
	return 24 * time.Hour // sensible default
}

// ParseSize parses a human-readable size string like "100MB", "1GB", "500KB"
// and returns the size in bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	// Find the boundary between digits and unit
	var numPart, unitPart string
	for i, c := range s {
		if c < '0' || c > '9' {
			numPart = s[:i]
			unitPart = strings.ToUpper(strings.TrimSpace(s[i:]))
			break
		}
	}
	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q: no numeric part", s)
	}

	val, err := strconv.ParseInt(numPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}

	var multiplier int64 = 1
	switch unitPart {
	case "B", "":
		multiplier = 1
	case "KB":
		multiplier = 1024
	case "MB":
		multiplier = 1024 * 1024
	case "GB":
		multiplier = 1024 * 1024 * 1024
	case "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size %q: unknown unit %q", s, unitPart)
	}

	return val * multiplier, nil
}
