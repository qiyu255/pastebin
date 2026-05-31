// Package idgen generates random IDs for pastes with collision detection.
package idgen

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
)

// Generator produces random string IDs from a character set.
// It maintains current length state and escalates length on collision exhaustion.
type Generator struct {
	charset    string
	minLen     int
	maxLen     int
	maxRetries int

	mu     sync.Mutex
	curLen int // current generation length
}

// New creates a new Generator.
func New(charset string, minLen, maxLen, initLen, maxRetries int) *Generator {
	if initLen < minLen {
		initLen = minLen
	}
	if initLen > maxLen {
		initLen = maxLen
	}
	return &Generator{
		charset:    charset,
		minLen:     minLen,
		maxLen:     maxLen,
		maxRetries: maxRetries,
		curLen:     initLen,
	}
}

// Generate produces a random ID and checks for collisions using the provided function.
// If the ID already exists (exists returns true), it retries up to the retry limit.
// If all retries are exhausted, length is increased for future generations.
// Returns an error if length would exceed maxLen.
func (g *Generator) Generate(exists func(id string) bool) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for attempt := 0; attempt < g.maxRetries; attempt++ {
		id, err := g.randomString(g.curLen)
		if err != nil {
			return "", err
		}
		if !exists(id) {
			return id, nil
		}
	}

	// Collision retries exhausted, increase length
	g.curLen++
	if g.curLen > g.maxLen {
		g.curLen-- // revert
		return "", fmt.Errorf("id generation: max length %d exceeded", g.maxLen)
	}

	// Try one more time with the new length
	id, err := g.randomString(g.curLen)
	if err != nil {
		return "", err
	}
	if !exists(id) {
		return id, nil
	}

	return "", fmt.Errorf("id generation: collision at length %d after escalation", g.curLen)
}

// CurLen returns the current generation length.
func (g *Generator) CurLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.curLen
}

// randomString generates a random string of the given length from the charset.
func (g *Generator) randomString(length int) (string, error) {
	b := make([]byte, length)
	charsetLen := big.NewInt(int64(len(g.charset)))

	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("crypto/rand: %w", err)
		}
		b[i] = g.charset[n.Int64()]
	}

	return string(b), nil
}
