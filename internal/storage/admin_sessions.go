package storage

import (
	"context"
	"errors"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrAdminSessionNotFound is returned when a session id does not exist.
var ErrAdminSessionNotFound = errors.New("admin session not found")

const adminSessionColumns = `sid::text, user_id::text, version, user_auth_version, status, refresh_hash, previous_refresh_hash, previous_refresh_valid_until, login_method, ip, user_agent, created_at, last_active_at, expires_at, revoked_at, revoke_reason`

func scanAdminSession(row pgx.Row) (model.AdminSession, error) {
	var s model.AdminSession
	if err := row.Scan(
		&s.SID, &s.UserID, &s.Version, &s.UserAuthVersion, &s.Status, &s.RefreshHash,
		&s.PreviousRefreshHash, &s.PreviousRefreshValidUntil,
		&s.LoginMethod, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastActiveAt,
		&s.ExpiresAt, &s.RevokedAt, &s.RevokeReason,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.AdminSession{}, ErrAdminSessionNotFound
		}
		return model.AdminSession{}, err
	}
	return s, nil
}

// CreateAdminSession stores a new session. The caller supplies the id so the
// refresh token can be derived from it before the row is written, which keeps
// session creation to a single INSERT. Only the refresh hash is persisted.
func (p *Postgres) CreateAdminSession(ctx context.Context, s model.AdminSession) (model.AdminSession, error) {
	q := `INSERT INTO gateway_admin_session
(sid, user_id, user_auth_version, refresh_hash, login_method, ip, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + adminSessionColumns
	return scanAdminSession(p.Pool.QueryRow(ctx, q,
		s.SID, s.UserID, s.UserAuthVersion, s.RefreshHash, s.LoginMethod, s.IP, s.UserAgent, s.ExpiresAt))
}

func (p *Postgres) FindAdminSessionByID(ctx context.Context, sid string) (model.AdminSession, error) {
	q := `SELECT ` + adminSessionColumns + ` FROM gateway_admin_session WHERE sid=$1 LIMIT 1`
	return scanAdminSession(p.Pool.QueryRow(ctx, q, sid))
}

// RotateAdminSessionRefresh replaces the refresh hash in place, parks the
// superseded hash in the previous slot, and reports whether this caller was the
// one holding the current hash.
//
// The compare-and-set is what makes rotation atomic: exactly one concurrent
// request wins. The losers are not treated as attackers — the service layer
// looks at the previous slot, which this statement fills, to recognise a benign
// race inside the grace window.
func (p *Postgres) RotateAdminSessionRefresh(ctx context.Context, sid, oldHash, newHash string, graceUntil time.Time) (bool, error) {
	tag, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_session
		 SET refresh_hash=$3, previous_refresh_hash=$2, previous_refresh_valid_until=$4, last_active_at=NOW()
		 WHERE sid=$1 AND refresh_hash=$2 AND status=$5 AND revoked_at IS NULL`,
		sid, oldHash, newHash, graceUntil, model.AdminSessionActive)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TouchAdminSession records activity. Best effort.
func (p *Postgres) TouchAdminSession(ctx context.Context, sid string) {
	_, _ = p.Pool.Exec(ctx, `UPDATE gateway_admin_session SET last_active_at=NOW() WHERE sid=$1`, sid)
}

// RevokeAdminSession marks a single session as revoked.
func (p *Postgres) RevokeAdminSession(ctx context.Context, sid, reason string) error {
	_, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_session SET status=$2, revoked_at=NOW(), revoke_reason=$3
		 WHERE sid=$1 AND revoked_at IS NULL`,
		sid, model.AdminSessionRevoked, reason)
	return err
}

// RevokeSessionsForUser revokes every active session of one account. Password
// changes and account disabling use it.
func (p *Postgres) RevokeSessionsForUser(ctx context.Context, userID, reason string) error {
	_, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_session SET status=$2, revoked_at=NOW(), revoke_reason=$3
		 WHERE user_id=$1 AND revoked_at IS NULL`,
		userID, model.AdminSessionRevoked, reason)
	return err
}

// ListAdminSessions returns the live sessions of one account, most recently
// active first.
func (p *Postgres) ListAdminSessions(ctx context.Context, userID string) ([]model.AdminSession, error) {
	q := `SELECT ` + adminSessionColumns + ` FROM gateway_admin_session
WHERE user_id=$1 AND status=$2 AND revoked_at IS NULL AND expires_at > NOW()
ORDER BY last_active_at DESC`
	rows, err := p.Pool.Query(ctx, q, userID, model.AdminSessionActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AdminSession
	for rows.Next() {
		s, err := scanAdminSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
