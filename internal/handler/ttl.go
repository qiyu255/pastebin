package handler

import (
	"time"

	"pastebin/internal/model"
)

// ttlForLength returns the TTL duration for a given ID length using the configured rules.
func ttlForLength(rules []model.TTLRule, length int) time.Duration {
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
