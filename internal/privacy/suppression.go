package privacy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SuppressionList tracks visitor cookieID-hashes that asked to opt out.
// In-memory hash set backed by a dedicated append-only WAL file — kept
// separate from the ingest WAL (B3-A4 review) so the consumer doesn't
// have to discriminate opt-out records from event records.
//
// Concurrency model: per-instance sync.RWMutex. Add() takes the write
// lock, fsyncs before returning. IsSuppressed() takes the read lock so
// the ingest hot path scales with cores.
type SuppressionList struct {
	mu  sync.RWMutex
	set map[string]struct{}

	wal *os.File
	buf *bufio.Writer
	now func() time.Time
}

type suppressionRecord struct {
	Hash string `json:"hash"`
	At   int64  `json:"at"`
}

// NewSuppressionList opens (or creates) walPath and replays it into the
// in-memory set. Returns an open list — caller MUST defer Close() so
// the bufio writer's buffer + the file handle are released. The
// parent directory MUST already exist.
func NewSuppressionList(walPath string) (*SuppressionList, error) {
	if walPath == "" {
		return nil, errors.New("suppression: wal path is empty")
	}

	dir := filepath.Dir(walPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("suppression: mkdir %s: %w", dir, err)
		}
	}

	list := &SuppressionList{
		set: make(map[string]struct{}),
		now: time.Now,
	}

	if err := list.replay(walPath); err != nil {
		return nil, fmt.Errorf("suppression: replay: %w", err)
	}

	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec // walPath is operator-controlled config (privacy.suppression_wal_path), validated empty above
	if err != nil {
		return nil, fmt.Errorf("suppression: open %s: %w", walPath, err)
	}

	list.wal = f
	list.buf = bufio.NewWriter(f)

	return list, nil
}

// replay reads the WAL on boot and rebuilds the in-memory set. Malformed
// lines are logged and skipped — partial WAL recovery is preferable to a
// boot-time failure when the file got truncated by a SIGKILL.
func (s *SuppressionList) replay(walPath string) error {
	f, err := os.Open(walPath) //nolint:gosec // operator-controlled config path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec suppressionRecord
		if jsonErr := json.Unmarshal(scanner.Bytes(), &rec); jsonErr != nil {
			continue
		}

		if rec.Hash == "" {
			continue
		}

		s.set[rec.Hash] = struct{}{}
	}

	return scanner.Err()
}

// Add appends a new opt-out record to the WAL, fsyncs, then inserts
// into the in-memory set. The fsync-before-set ordering means an
// operator crash after the syscall returns can never leave the visitor
// in a state where the cookie they were promised takes effect but
// future events still land.
//
// hash MUST be the "h:" + hex SHA-256 form produced by
// identity.HexCookieIDHash. The list does not enforce the prefix —
// callers should pre-normalize so the comparison key matches what
// ingest writes to events_raw.cookie_id.
func (s *SuppressionList) Add(hash string) error {
	if hash == "" {
		return errors.New("suppression: empty hash")
	}

	rec := suppressionRecord{Hash: hash, At: s.now().Unix()}

	body, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("suppression: marshal: %w", err)
	}

	body = append(body, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.buf.Write(body); err != nil {
		return fmt.Errorf("suppression: write: %w", err)
	}

	if err := s.buf.Flush(); err != nil {
		return fmt.Errorf("suppression: flush: %w", err)
	}

	// fsync — Privacy Rule 1's analogue for suppression durability.
	// A crash between user click and disk would silently strand the
	// visitor in a half-opted-out state and break the GDPR Art. 21
	// contract; the syscall must succeed before we ack.
	if err := s.wal.Sync(); err != nil {
		return fmt.Errorf("suppression: fsync: %w", err)
	}

	s.set[hash] = struct{}{}

	return nil
}

// IsSuppressed checks the in-memory set in read-lock mode. Safe to
// call from the ingest hot path; lookup is sub-µs.
func (s *SuppressionList) IsSuppressed(hash string) bool {
	if hash == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.set[hash]

	return ok
}

// Len reports the number of suppressed visitors — useful for metrics
// and the admin overview. RLock-protected.
func (s *SuppressionList) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.set)
}

// Close flushes the bufio writer and closes the WAL file. Idempotent
// to the extent os.File.Close is.
func (s *SuppressionList) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error

	if s.buf != nil {
		if err := s.buf.Flush(); err != nil {
			firstErr = err
		}
	}

	if s.wal != nil {
		if err := s.wal.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}

		if err := s.wal.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// Compile-time guard: io.Closer compliance.
var _ io.Closer = (*SuppressionList)(nil)
