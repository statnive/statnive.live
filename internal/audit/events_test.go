package audit_test

import (
	"regexp"
	"testing"

	"github.com/statnive/statnive.live/internal/audit"
)

// dottedLowercase matches the project convention documented in
// events.go: lowercase ASCII segments joined by dots, two or more
// segments (e.g. "privacy.opt_out_received", "auth.login.success").
// Underscores are allowed in non-leading segments.
var dottedLowercase = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)

func TestPrivacyAndLegalConstants_DottedLowercase(t *testing.T) {
	t.Parallel()

	got := []audit.EventName{
		audit.EventOptOutReceived,
		audit.EventDSARAccessRequested,
		audit.EventDSAREraseRequested,
		audit.EventConsentGiven,
		audit.EventConsentWithdrawn,
		audit.EventLIAViewed,
		audit.EventDPAViewed,
		audit.EventPrivacyPolicyViewed,
	}
	for _, name := range got {
		if !dottedLowercase.MatchString(string(name)) {
			t.Errorf("event %q does not match ^[a-z]+\\.[a-z_]+$", name)
		}
	}
}
