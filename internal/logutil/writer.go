// Package logutil provides a rotating file writer for slog.
package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const currentLogName = "pastebin.log"

// RotationMode specifies when log files are rotated.
type RotationMode string

const (
	RotationTime RotationMode = "time"
	RotationSize RotationMode = "size"
)

// RotatingWriter implements io.Writer with log rotation and retention.
// It writes to a current log file and rotates based on time or size.
type RotatingWriter struct {
	dir        string
	mode       RotationMode
	maxAge     time.Duration // for time rotation
	maxSize    int64         // for size rotation
	maxBackups int           // for size rotation

	mu       sync.Mutex
	file     *os.File
	curSize  int64  // bytes written to current file
	openDate string // YYYY-MM-DD, for time rotation
}

// New creates a RotatingWriter. If dir is empty, returns nil (caller should use stderr).
func New(dir string, mode RotationMode, maxAge time.Duration, maxSize int64, maxBackups int) (*RotatingWriter, error) {
	if dir == "" {
		return nil, nil
	}

	// Ensure log directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir %q: %w", dir, err)
	}

	rw := &RotatingWriter{
		dir:        dir,
		mode:       mode,
		maxAge:     maxAge,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}

	if err := rw.openFile(); err != nil {
		return nil, err
	}

	return rw, nil
}

// Write implements io.Writer. It checks rotation conditions before writing.
func (rw *RotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	// Check rotation
	switch rw.mode {
	case RotationTime:
		today := time.Now().UTC().Format("2006-01-02")
		if today != rw.openDate {
			if err := rw.rotate(); err != nil {
				return 0, err
			}
		}
	case RotationSize:
		if rw.maxSize > 0 && rw.curSize+int64(len(p)) > rw.maxSize {
			if err := rw.rotate(); err != nil {
				return 0, err
			}
		}
	}

	n, err := rw.file.Write(p)
	rw.curSize += int64(n)
	return n, err
}

// Close flushes and closes the current log file.
func (rw *RotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.file != nil {
		err := rw.file.Close()
		rw.file = nil
		return err
	}
	return nil
}

// openFile opens (or creates) the current log file.
func (rw *RotatingWriter) openFile() error {
	path := filepath.Join(rw.dir, currentLogName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file %q: %w", path, err)
	}

	// Get current file size
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log file %q: %w", path, err)
	}

	rw.file = f
	rw.curSize = info.Size()
	rw.openDate = time.Now().UTC().Format("2006-01-02")
	return nil
}

// rotate closes the current file, renames it, opens a new one, and cleans up old files.
func (rw *RotatingWriter) rotate() error {
	if rw.file == nil {
		return nil
	}

	// Close current file
	if err := rw.file.Close(); err != nil {
		return fmt.Errorf("close log for rotation: %w", err)
	}
	rw.file = nil

	// Rename current log to archive name
	currentPath := filepath.Join(rw.dir, currentLogName)
	var archiveName string
	switch rw.mode {
	case RotationTime:
		// Use the date when the file was opened (not today, since it's just rolled over)
		archiveName = fmt.Sprintf("pastebin-%s.log", rw.openDate)
	case RotationSize:
		archiveName = fmt.Sprintf("pastebin-%d.log", time.Now().UnixMilli())
	}

	archivePath := filepath.Join(rw.dir, archiveName)
	if err := os.Rename(currentPath, archivePath); err != nil {
		return fmt.Errorf("rename log for rotation: %w", err)
	}

	// Clean up old files
	rw.cleanup()

	// Open new file
	return rw.openFile()
}

// cleanup removes old log files based on rotation mode.
func (rw *RotatingWriter) cleanup() {
	switch rw.mode {
	case RotationTime:
		if rw.maxAge > 0 {
			rw.cleanupByAge()
		}
	case RotationSize:
		if rw.maxBackups > 0 {
			rw.cleanupByCount()
		}
	}
}

// cleanupByAge removes log files older than maxAge.
func (rw *RotatingWriter) cleanupByAge() {
	entries, err := os.ReadDir(rw.dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-rw.maxAge)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only clean up archived log files, not the current one
		if !strings.HasPrefix(name, "pastebin-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(rw.dir, name))
		}
	}
}

// cleanupByCount removes old log files exceeding maxBackups.
func (rw *RotatingWriter) cleanupByCount() {
	entries, err := os.ReadDir(rw.dir)
	if err != nil {
		return
	}

	var backups []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "pastebin-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		backups = append(backups, entry)
	}

	if len(backups) <= rw.maxBackups {
		return
	}

	// Sort by name (older timestamps sort first)
	sort.Slice(backups, func(i, j int) bool {
		infoI, _ := backups[i].Info()
		infoJ, _ := backups[j].Info()
		if infoI != nil && infoJ != nil {
			return infoI.ModTime().Before(infoJ.ModTime())
		}
		return backups[i].Name() < backups[j].Name()
	})

	// Delete oldest files
	for i := 0; i < len(backups)-rw.maxBackups; i++ {
		os.Remove(filepath.Join(rw.dir, backups[i].Name()))
	}
}
