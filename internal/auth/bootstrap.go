package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/statnive/statnive.live/internal/audit"
)

// BootstrapConfig controls first-run admin creation. If Email or
// Password is empty the bootstrap is skipped — the operator can still
// create users later via the admin API (Phase 3c). When a value is
// supplied, Bootstrap is idempotent: existing users skip the insert.
// SitesStore is optional; when non-nil, the freshly-created admin gets
// an admin grant on SiteID in user_sites so the per_site_admin flag
// can be flipped on without a manual SQL insert (fixes the "bad site_id /
// 403 on first admin probe" smoke regression).
type BootstrapConfig struct {
	Email      string
	Password   string
	Username   string // defaults to "admin" if empty
	SiteID     uint32
	BcryptCost int
	SitesStore SitesStore // optional — when set, also writes user_sites grant
}

// Bootstrap creates the first admin user if the users table is empty
// for SiteID. Called from main.go after migrations apply. Returns nil
// if creation succeeded, or if the bootstrap was a no-op (config empty
// or users already exist).
func Bootstrap(
	ctx context.Context, store Store, cfg BootstrapConfig,
	auditLog *audit.Logger, logger *slog.Logger,
) error {
	if store == nil {
		return errors.New("auth: bootstrap requires a store")
	}

	cfg.Email = strings.ToLower(strings.TrimSpace(cfg.Email))

	if cfg.Email == "" || cfg.Password == "" {
		logger.Warn("auth bootstrap skipped — STATNIVE_BOOTSTRAP_ADMIN_{EMAIL,PASSWORD} not set; create users via admin API once available")

		return nil
	}

	if cfg.BcryptCost < MinBcryptCost {
		cfg.BcryptCost = MinBcryptCost
	}

	if cfg.Username == "" {
		cfg.Username = "admin"
	}

	existing, _, err := store.GetUserByEmail(ctx, cfg.SiteID, cfg.Email)
	if err == nil && existing != nil {
		// User already present — bootstrap is idempotent. Don't touch
		// the password.
		return nil
	}

	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("bootstrap lookup: %w", err)
	}

	hash, err := HashPassword(cfg.Password, cfg.BcryptCost)
	if err != nil {
		return fmt.Errorf("bootstrap hash: %w", err)
	}

	u := &User{
		UserID:   uuid.New(),
		SiteID:   cfg.SiteID,
		Email:    cfg.Email,
		Username: cfg.Username,
		Role:     RoleAdmin,
	}

	if createErr := store.CreateUser(ctx, u, hash); createErr != nil {
		return fmt.Errorf("bootstrap create: %w", createErr)
	}

	grantBootstrapSite(ctx, cfg, u.UserID, logger)

	if auditLog != nil {
		auditLog.Event(ctx, audit.EventAuthBootstrap,
			slog.String("email_hash", hashForAudit(cfg.Email)),
			slog.Uint64("site_id", uint64(cfg.SiteID)),
			// admin_id is the admin user's internal UUID — not a tracker
			// visitor user_id. Keyed distinctly so the privacy gate can
			// distinguish the two.
			slog.String("admin_id", u.UserID.String()),
		)
	}

	logger.Info("auth bootstrap created first admin",
		"site_id", cfg.SiteID,
		"username", cfg.Username,
	)

	return nil
}

// grantBootstrapSite gives the bootstrap admin an admin grant on their
// bootstrap site_id in user_sites — required so flipping
// STATNIVE_FEATURES_PER_SITE_ADMIN=true works on fresh deploys without
// a manual SQL insert. Failure is non-fatal: the user row already exists
// and the operator can add the grant manually via SQL. Hoisted out of
// Bootstrap so the function stays under gocyclo's complexity floor.
func grantBootstrapSite(
	ctx context.Context, cfg BootstrapConfig, userID uuid.UUID, logger *slog.Logger,
) {
	if cfg.SitesStore == nil {
		return
	}

	if err := cfg.SitesStore.Grant(ctx, userID, cfg.SiteID, RoleAdmin); err != nil {
		logger.Warn("auth bootstrap user_sites grant failed — operator must add grant manually",
			"site_id", cfg.SiteID,
			"err", err,
		)
	}
}
