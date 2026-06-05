package privacy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// EraseTokensByUserID hard-deletes a dashboard user's self-serve MCP tokens
// for a DSAR / account erasure. This is USER-scoped (by user_id) — distinct
// from EraseByCookieID, which is VISITOR-scoped (cookie_id + site_id).
//
// MCP tokens are user-linked PII, but mcp_tokens carries no cookie_id column,
// so the cookie-based enumerator never touches it (correctly — it is not
// visitor data). Account erasure must call this explicitly. Closes the
// dsar-completeness-checker gap for the mcp_tokens table; PR-E extends it to
// oauth_refresh_tokens.
//
// Uses ALTER ... DELETE (a mutation); rows disappear at the next merge.
// mutations_sync is left unset to match EraseByCookieID — callers that need
// synchronous completion poll via WaitForCompletion.
func (e *EraseEnumerator) EraseTokensByUserID(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return errors.New("erase tokens: nil user id")
	}

	q := fmt.Sprintf("ALTER TABLE %s.mcp_tokens DELETE WHERE user_id = ?", e.database)
	if err := e.conn.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("erase mcp_tokens for user: %w", err)
	}

	return nil
}
