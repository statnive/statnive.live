package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// JurisdictionNoticeStore reads / writes the per-user one-time admin
// notice flag (migration 014). Two methods keep the surface narrow so
// the future SPA Compliance-card flow doesn't have to drag the full
// auth.Store interface around.
//
// The "notice" is the Stage-3 prompt that asks operators of pre-Stage-3
// sites (the 3 live operators, plus anyone whose sites backfilled to
// OTHER-NON-EU + permissive) to consciously pick a jurisdiction. The
// dashboard surfaces it once per user; dismissing it persists across
// logins.
type JurisdictionNoticeStore interface {
	IsJurisdictionNoticeDismissed(ctx context.Context, userID uuid.UUID) (bool, error)
	DismissJurisdictionNotice(ctx context.Context, userID uuid.UUID) error
}

// IsJurisdictionNoticeDismissed reads the flag via SELECT FINAL. The
// ReplacingMergeTree FINAL converges on the latest write so a freshly
// dismissed flag is visible on the next dashboard refresh without
// waiting for a background merge. Returns false on ErrNotFound — a
// brand-new user has never dismissed anything.
func (s *ClickHouseStore) IsJurisdictionNoticeDismissed(
	ctx context.Context, userID uuid.UUID,
) (bool, error) {
	if userID == uuid.Nil {
		return false, fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	var flag uint8

	row := s.conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT jurisdiction_notice_dismissed FROM %s.users FINAL
		 WHERE user_id = ? LIMIT 1`,
		s.db,
	), userID)

	if err := row.Scan(&flag); err != nil {
		// Treat any scan failure as "not dismissed" — the worst case
		// is the user sees the notice once more, which is graceful
		// next to a 500. Connection failures still surface through
		// the request-level health gates.
		return false, nil //nolint:nilerr // intentional graceful fallback documented above
	}

	return flag != 0, nil
}

// DismissJurisdictionNotice flips the column to 1 via ALTER TABLE
// UPDATE. mutations_sync = 2 makes the change visible to the next
// SELECT FINAL by the caller without a background-merge wait. The
// users table has at most a few rows per tenant so the per-mutation
// cost is sub-millisecond.
func (s *ClickHouseStore) DismissJurisdictionNotice(
	ctx context.Context, userID uuid.UUID,
) error {
	if userID == uuid.Nil {
		return fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	if err := s.conn.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE %s.users UPDATE jurisdiction_notice_dismissed = 1
		 WHERE user_id = ? SETTINGS mutations_sync = 2`,
		s.db,
	), userID); err != nil {
		return fmt.Errorf("dismiss jurisdiction notice: %w", err)
	}

	return nil
}
