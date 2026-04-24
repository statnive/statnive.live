// should-not-trigger: slog calls that correctly wrap PII in redaction
// helpers. Every call site below MUST NOT be flagged by slog-no-raw-pii.
package shouldnottrigger

import "log/slog"

// Stand-ins for the real helpers; the rule matches by package+function
// name, not by type.
type identityNS struct{}
type redactNS struct{}

func (identityNS) HexUserIDHash(_ string) string { return "" }
func (identityNS) HashIP(_ string) uint64        { return 0 }
func (redactNS) Email(_ string) string           { return "" }

var identity = identityNS{}
var redact = redactNS{}

func logHashedUserID(uid string) {
	slog.Info("login", "user_id", identity.HexUserIDHash(uid))
}

func logHashedIP(ip string) {
	slog.Warn("rate limited", "ip", identity.HashIP(ip))
}

func logRedactedEmail(addr string) {
	slog.Info("signup", "email", redact.Email(addr))
}

func logConstantKeyValue() {
	// Compile-time constant values are allowed — can't leak PII.
	slog.Info("boot", "user_id", "bootstrap-admin")
}