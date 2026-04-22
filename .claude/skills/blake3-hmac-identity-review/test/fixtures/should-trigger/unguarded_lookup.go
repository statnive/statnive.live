// should-trigger: unguarded LookupSession call site.
// The semgrep rule auth-return-nil-guard MUST flag this file.
package shouldtrigger

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

// UnguardedCallSite dereferences info.UserID without checking info != nil
// after err == nil. A fault-injected (nil, nil) return causes a nil-ptr panic.
func UnguardedCallSite(ctx context.Context, store fakeStore) string {
	info, err := store.LookupSession(ctx, [32]byte{})
	if err != nil {
		return ""
	}

	return info.UserID
}
