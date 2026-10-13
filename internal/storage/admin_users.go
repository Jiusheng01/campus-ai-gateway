package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrAdminUserNotFound is returned when no account matches the lookup. It is a
// sentinel so callers never have to match on error strings.
var ErrAdminUserNotFound = errors.New("admin user not found")

const adminUserColumns = `id::text, username, password_hash, display_name, roles, status, auth_version, last_login_at, created_at, updated_at`

func scanAdminUser(row pgx.Row) (model.AdminUser, error) {
	var u model.AdminUser
	var roles []byte
	if err := row.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &roles,
		&u.Status, &u.AuthVersion, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.AdminUser{}, ErrAdminUserNotFound
		}
		return model.AdminUser{}, err
	}
	_ = json.Unmarshal(roles, &u.Roles)
	return u, nil
}

// CountAdminUsers reports how many accounts exist. First-run setup and the
// environment bootstrap both use it to decide whether creating one is allowed.
func (p *Postgres) CountAdminUsers(ctx context.Context) (int64, error) {
	var n int64
	err := p.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_admin_user`).Scan(&n)
	return n, err
}

// FindAdminUserByUsername matches case-insensitively, mirroring the unique
// index on lower(username).
func (p *Postgres) FindAdminUserByUsername(ctx context.Context, username string) (model.AdminUser, error) {
	q := `SELECT ` + adminUserColumns + ` FROM gateway_admin_user WHERE lower(username)=lower($1) LIMIT 1`
	return scanAdminUser(p.Pool.QueryRow(ctx, q, username))
}

func (p *Postgres) FindAdminUserByID(ctx context.Context, id string) (model.AdminUser, error) {
	q := `SELECT ` + adminUserColumns + ` FROM gateway_admin_user WHERE id=$1 LIMIT 1`
	return scanAdminUser(p.Pool.QueryRow(ctx, q, id))
}

// firstAdminAdvisoryLockKey is a fixed advisory-lock key shared by every
// gateway instance. It serialises the count-then-insert sequence that creates
// the very first administrator.
//
// The value is arbitrary but must never change: it is the rendezvous point
// between replicas. It spells "campusad" in ASCII.
const firstAdminAdvisoryLockKey int64 = 0x63616D7075736164

// CreateFirstAdminUser inserts the very first account.
//
// A bare `INSERT ... WHERE NOT EXISTS` is not enough here: under READ COMMITTED
// two concurrent transactions can both observe an empty table and both insert,
// which would leave two super administrators behind. The whole sequence runs
// inside one transaction that first takes a session-scoped advisory lock, so
// the second caller blocks until the first commits and then sees count > 0.
//
// It returns ErrAdminUserNotFound when an account already exists, which the
// service layer maps to "setup is no longer allowed".
func (p *Postgres) CreateFirstAdminUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error) {
	encoded, err := json.Marshal(roles)
	if err != nil {
		return model.AdminUser{}, err
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.AdminUser{}, err
	}
	// Rollback after a successful commit is a no-op, so this is safe as the
	// single cleanup path.
	defer func() { _ = tx.Rollback(ctx) }()

	// The lock is released when the transaction ends, so a crashed caller can
	// never wedge first-run setup.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, firstAdminAdvisoryLockKey); err != nil {
		return model.AdminUser{}, err
	}

	var existing int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_admin_user`).Scan(&existing); err != nil {
		return model.AdminUser{}, err
	}
	if existing > 0 {
		return model.AdminUser{}, ErrAdminUserNotFound
	}

	q := `INSERT INTO gateway_admin_user (username, display_name, password_hash, roles)
VALUES ($1, $2, $3, $4::jsonb)
RETURNING ` + adminUserColumns
	user, err := scanAdminUser(tx.QueryRow(ctx, q, username, displayName, passwordHash, encoded))
	if err != nil {
		return model.AdminUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.AdminUser{}, err
	}
	return user, nil
}

// UpdateAdminUserPassword rotates the hash and bumps auth_version in the same
// statement, which invalidates every access token and session issued before
// the change.
func (p *Postgres) UpdateAdminUserPassword(ctx context.Context, id, passwordHash string) error {
	tag, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_user SET password_hash=$2, auth_version=auth_version+1, updated_at=NOW() WHERE id=$1`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAdminUserNotFound
	}
	return nil
}

// RehashAdminUserPassword replaces the stored hash without touching
// auth_version. It backs the transparent upgrade from bcrypt (or from weak
// Argon2id parameters) to the current policy during login: rewriting the hash
// must not sign the user out of their other devices.
func (p *Postgres) RehashAdminUserPassword(ctx context.Context, id, passwordHash string) error {
	tag, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_user SET password_hash=$2, updated_at=NOW() WHERE id=$1`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAdminUserNotFound
	}
	return nil
}

// TouchAdminUserLogin records a successful login. Best effort: a failure here
// must never turn a valid login into an error.
func (p *Postgres) TouchAdminUserLogin(ctx context.Context, id string) {
	_, _ = p.Pool.Exec(ctx, `UPDATE gateway_admin_user SET last_login_at=NOW() WHERE id=$1`, id)
}
