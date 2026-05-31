// Package store provides SQLite-backed paste storage.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"pastebin/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

// Store manages paste persistence in SQLite.
type Store struct {
	db     *sql.DB
	dbPath string
}

// Open opens the SQLite database, creates the pastes table if needed,
// and enables WAL mode.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// migrate creates the pastes table if it doesn't exist.
func (s *Store) migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS pastes (
		id         TEXT PRIMARY KEY,
		content    BLOB,
		size       INTEGER NOT NULL DEFAULT 0,
		locked     INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		expired_at INTEGER NOT NULL,
		created_by TEXT NOT NULL DEFAULT '',
		updated_by TEXT NOT NULL DEFAULT ''
	)`
	_, err := s.db.Exec(query)
	return err
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Get retrieves a paste by ID. Returns nil if not found or expired.
func (s *Store) Get(id string) (*model.Paste, error) {
	now := time.Now().UnixMilli()
	row := s.db.QueryRow(
		`SELECT id, content, size, locked, created_at, updated_at, expired_at, created_by, updated_by
		 FROM pastes WHERE id = ? AND expired_at > ?`, id, now)

	var p model.Paste
	var locked int
	err := row.Scan(&p.ID, &p.Content, &p.Size, &locked, &p.CreatedAt, &p.UpdatedAt, &p.ExpiredAt, &p.CreatedBy, &p.UpdatedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get paste %q: %w", id, err)
	}
	p.Locked = locked != 0
	return &p, nil
}

// Set inserts or replaces a paste.
func (s *Store) Set(p *model.Paste) error {
	locked := 0
	if p.Locked {
		locked = 1
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO pastes (id, content, size, locked, created_at, updated_at, expired_at, created_by, updated_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Content, p.Size, locked, p.CreatedAt, p.UpdatedAt, p.ExpiredAt, p.CreatedBy, p.UpdatedBy)
	if err != nil {
		return fmt.Errorf("set paste %q: %w", p.ID, err)
	}
	return nil
}

// Delete removes a paste by ID.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM pastes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete paste %q: %w", id, err)
	}
	return nil
}

// Exists reports whether a non-expired paste with the given ID exists.
func (s *Store) Exists(id string) (bool, error) {
	now := time.Now().UnixMilli()
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM pastes WHERE id = ? AND expired_at > ?`, id, now).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("exists %q: %w", id, err)
	}
	return true, nil
}

// Lock sets a paste as locked (no expiry, no overwrite).
func (s *Store) Lock(id string, updatedBy string, updatedAt int64) error {
	_, err := s.db.Exec(
		`UPDATE pastes SET locked = 1, expired_at = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		model.MaxExpiredAt, updatedAt, updatedBy, id)
	if err != nil {
		return fmt.Errorf("lock paste %q: %w", id, err)
	}
	return nil
}

// Unlock removes the lock and resets the expiry time.
func (s *Store) Unlock(id string, expiredAt int64, updatedBy string, updatedAt int64) error {
	_, err := s.db.Exec(
		`UPDATE pastes SET locked = 0, expired_at = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		expiredAt, updatedAt, updatedBy, id)
	if err != nil {
		return fmt.Errorf("unlock paste %q: %w", id, err)
	}
	return nil
}

// DeleteExpired removes all pastes whose expired_at is <= now and that are not locked.
func (s *Store) DeleteExpired() (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.Exec(`DELETE FROM pastes WHERE expired_at <= ? AND locked = 0`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Count returns the number of non-expired pastes.
func (s *Store) Count() (int64, error) {
	now := time.Now().UnixMilli()
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pastes WHERE expired_at > ?`, now).Scan(&n)
	return n, err
}

// TotalSize returns the sum of sizes of all non-expired pastes.
func (s *Store) TotalSize() (int64, error) {
	now := time.Now().UnixMilli()
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM pastes WHERE expired_at > ?`, now).Scan(&n)
	return n, err
}

// RecentPastes returns the N most recently updated non-expired pastes.
func (s *Store) RecentPastes(limit int) ([]model.RecentPaste, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.Query(
		`SELECT id, updated_at FROM pastes WHERE expired_at > ? ORDER BY updated_at DESC LIMIT ?`,
		now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pastes []model.RecentPaste
	for rows.Next() {
		var rp model.RecentPaste
		if err := rows.Scan(&rp.ID, &rp.UpdatedAt); err != nil {
			return nil, err
		}
		pastes = append(pastes, rp)
	}
	return pastes, rows.Err()
}

// DBSize returns the current size of the database file in bytes.
func (s *Store) DBSize() (int64, error) {
	info, err := os.Stat(s.dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// GetOldSize retrieves the size of a paste before it's overwritten, for usage tracking.
// Returns 0 if the paste doesn't exist or is expired.
func (s *Store) GetOldSize(id string) (int64, error) {
	now := time.Now().UnixMilli()
	var size int64
	err := s.db.QueryRow(`SELECT size FROM pastes WHERE id = ? AND expired_at > ?`, id, now).Scan(&size)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return size, nil
}
