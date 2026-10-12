package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrAdminUserNotFound is returned when a lookup by username or id matches no
// row. Callers use it to distinguish "wrong credentials" from a real database
// failure, so a transient outage is never reported as a bad password.
var ErrAdminUserNotFound = errors.New("admin user not found")

const adminUserColumns = `id::text, username, password_hash, display_name, roles, status, last_login_at, created_at`

func scanAdminUser(row pgx.Row) (model.AdminUser, error) {
	var u model.AdminUser
	var roles []byte
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &roles, &u.Status, &u.LastLoginAt, &u.CreatedAt)
	if err != nil {
		return model.AdminUser{}, err
	}
	_ = json.Unmarshal(roles, &u.Roles)
	return u, nil
}

// CountAdminUsers reports how many local accounts exist. The login UI uses this
// to decide between showing the first-run setup wizard and showing the login
// form.
func (p *Postgres) CountAdminUsers(ctx context.Context) (int64, error) {
	var n int64
	err := p.Pool.QueryRow(ctx, `SELECT COUNT(*)::bigint FROM gateway_admin_user`).Scan(&n)
	return n, err
}

func (p *Postgres) FindAdminUserByUsername(ctx context.Context, username string) (model.AdminUser, error) {
	q := `SELECT ` + adminUserColumns + ` FROM gateway_admin_user WHERE lower(username)=lower($1) LIMIT 1`
	u, err := scanAdminUser(p.Pool.QueryRow(ctx, q, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.AdminUser{}, ErrAdminUserNotFound
	}
	return u, err
}

// FindAdminUserByID is called on every authenticated admin request so that a
// disabled account loses access immediately instead of keeping a live session
// until it expires.
func (p *Postgres) FindAdminUserByID(ctx context.Context, id string) (model.AdminUser, error) {
	q := `SELECT ` + adminUserColumns + ` FROM gateway_admin_user WHERE id=$1::uuid LIMIT 1`
	u, err := scanAdminUser(p.Pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.AdminUser{}, ErrAdminUserNotFound
	}
	return u, err
}

// CreateAdminUser inserts an account. The caller supplies an already-hashed
// password; plaintext must never reach this layer.
func (p *Postgres) CreateAdminUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error) {
	if roles == nil {
		roles = []string{"auditor"}
	}
	encoded, err := json.Marshal(roles)
	if err != nil {
		return model.AdminUser{}, err
	}
	q := `INSERT INTO gateway_admin_user(username, password_hash, display_name, roles)
VALUES ($1, $2, $3, $4)
RETURNING ` + adminUserColumns
	return scanAdminUser(p.Pool.QueryRow(ctx, q, username, passwordHash, displayName, encoded))
}

// CreateFirstAdminUser inserts the very first account only when the table is
// still empty. The guard lives in the INSERT itself rather than in a preceding
// COUNT so two concurrent setup requests cannot both succeed.
func (p *Postgres) CreateFirstAdminUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error) {
	if roles == nil {
		roles = []string{"super_admin"}
	}
	encoded, err := json.Marshal(roles)
	if err != nil {
		return model.AdminUser{}, err
	}
	q := `INSERT INTO gateway_admin_user(username, password_hash, display_name, roles)
SELECT $1, $2, $3, $4
WHERE NOT EXISTS (SELECT 1 FROM gateway_admin_user)
RETURNING ` + adminUserColumns
	u, err := scanAdminUser(p.Pool.QueryRow(ctx, q, username, passwordHash, displayName, encoded))
	if errors.Is(err, pgx.ErrNoRows) {
		// The table was no longer empty when the statement ran.
		return model.AdminUser{}, ErrAdminUserNotFound
	}
	return u, err
}

func (p *Postgres) UpdateAdminUserPassword(ctx context.Context, id, passwordHash string) error {
	_, err := p.Pool.Exec(ctx,
		`UPDATE gateway_admin_user SET password_hash=$2, updated_at=NOW() WHERE id=$1::uuid`,
		id, passwordHash)
	return err
}

func (p *Postgres) TouchAdminUserLogin(ctx context.Context, id string) {
	_, _ = p.Pool.Exec(ctx,
		`UPDATE gateway_admin_user SET last_login_at=NOW() WHERE id=$1::uuid`, id)
}
