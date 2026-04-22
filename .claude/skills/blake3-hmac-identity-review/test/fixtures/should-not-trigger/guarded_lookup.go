// should-not-trigger: correctly guarded LookupSession call site.
// The semgrep rule auth-return-nil-guard MUST NOT flag this file.
package shouldnottrigger

import (
	"context"
)

type fakeStore struct{}

type sessionInfo struct {
	UserID string
}

func (fakeStore) LookupSession(_ context.Context, _ [32]byte) (*sessionInfo, error) {
	return nil, nil
}

// GuardedCallSite checks info != nil AFTER err == nil, so a fault-injected
// (nil, nil) is caught and rejected without a nil-ptr dereference.
func GuardedCallSite(ctx context.Context, store fakeStore) string {
	info, err := store.LookupSession(ctx, [32]byte{})
	if err != nil {
		return ""
	}

	if info == nil {
		return ""
	}

	return info.UserID
}
