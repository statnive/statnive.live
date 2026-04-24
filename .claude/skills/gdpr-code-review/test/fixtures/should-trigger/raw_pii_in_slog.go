// should-trigger: slog calls that leak raw PII into the audit log.
// Every call site below MUST be flagged by slog-no-raw-pii.
package shouldtrigger

import "log/slog"

func logRawUserID(uid string) {
	slog.Info("login", "user_id", uid)
}

func logRawIP(ip string) {
	slog.Warn("rate limited", "ip", ip)
}

func logRawRemoteAddr(addr string) {
	slog.Info("request", "remote_addr", addr)
}

func logRawEmail(addr string) {
	slog.Info("signup", "email", addr)
}

func logMasterSecret(s string) {
	slog.Debug("boot", "master_secret", s)
}

func logRawViaLogger(lg *slog.Logger, uid string) {
	lg.Info("event", "user_id", uid)
}