package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrAdminUserNotFound is returned when no account matches the lookup. It is a
// sentinel so callers never have to match on error strings.
var ErrAdminUserNotFound = errors.New("admin user not found")

// ErrAdminUsernameTaken is returned when the username collides with an existing
// account. Usernames are matched case-insensitively, so "Zhang" and "zhang" are
// the same name. Callers surface a business error rather than a database error.
var ErrAdminUsernameTaken = errors.New("username is already taken")

// ErrLastSuperAdmin is returned when a change would leave the system without a
// single enabled super administrator. It is enforced here, in the same
// transaction as the write, because a check in the service layer could be raced
// by two concurrent demotions.
var ErrLastSuperAdmin = errors.New("the last enabled super administrator cannot be removed")

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

// superAdminPredicate matches accounts holding the super_admin role. It is a
// containment test on the JSONB array rather than a string match, so a role
// list like ["member","super_admin"] is still recognised.
const superAdminPredicate = `roles @> '["super_admin"]'::jsonb`

// CountSuperAdmins reports how many super administrators exist, enabled or not.
//
// First-run setup keys off this rather than off "are there any accounts at all":
// a self-registered member must never be able to close the setup window, and a
// disabled super administrator must not reopen it either. Both would be ways to
// end up with a running gateway that nobody can administer.
func (p *Postgres) CountSuperAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := p.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_admin_user WHERE `+superAdminPredicate).Scan(&n)
	return n, err
}

// managementRolePredicate matches accounts holding at least one role that
// grants control-plane access.
//
// The role list mirrors managementRoles in internal/app/account_roles.go,
// which is the source of truth the gateway's allow-list is built from. It is
// duplicated here because storage cannot import app (the dependency runs the
// other way), so the comment is the contract: a change to the role semantics
// must change both. tenant_admin is deliberately absent — Phase 1 withdrew its
// global grant, so an account holding only that role grants nothing and is
// counted as a member, exactly as the gateway treats it.
const managementRolePredicate = `EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(roles) AS r
    WHERE r IN ('auditor','operator','gateway_admin','super_admin')
  )`

// AdminUserStats returns the counts shown on the user management page. They are
// computed in one statement so they cannot disagree with each other.
func (p *Postgres) AdminUserStats(ctx context.Context) (model.AdminUserStats, error) {
	var out model.AdminUserStats
	err := p.Pool.QueryRow(ctx, `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE NOT `+managementRolePredicate+`),
  COUNT(*) FILTER (WHERE `+managementRolePredicate+`),
  COUNT(*) FILTER (WHERE status <> 1)
FROM gateway_admin_user
`).Scan(&out.Total, &out.Members, &out.Admins, &out.Disabled)
	return out, err
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

// ListAdminUsers returns accounts newest first. The limit is applied by the
// caller so a runaway account table cannot be pulled into memory in one go.
func (p *Postgres) ListAdminUsers(ctx context.Context, limit int) ([]model.AdminUser, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q := `SELECT ` + adminUserColumns + ` FROM gateway_admin_user ORDER BY created_at DESC, id DESC LIMIT $1`
	rows, err := p.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.AdminUser, 0, 32)
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// firstAdminAdvisoryLockKey is a fixed advisory-lock key shared by every
// gateway instance. It serialises the count-then-insert sequence that creates
// the very first administrator.
//
// The value is arbitrary but must never change: it is the rendezvous point
// between replicas. It spells "campusad" in ASCII.
const firstAdminAdvisoryLockKey int64 = 0x63616D7075736164

// adminUserGuardLockKey serialises every change that could remove the last
// enabled super administrator — role edits and status changes alike. It spells
// "campusus" in ASCII and must never change for the same reason as the key
// above: it is how replicas agree on who goes first.
const adminUserGuardLockKey int64 = 0x63616D7075737573

// CreateFirstAdminUser inserts the very first account.
//
// A bare `INSERT ... WHERE NOT EXISTS` is not enough here: under READ COMMITTED
// two concurrent transactions can both observe an empty table and both insert,
// which would leave two super administrators behind. The whole sequence runs
// inside one transaction that first takes a session-scoped advisory lock, so
// the second caller blocks until the first commits and then sees count > 0.
//
// The guard is "does a super administrator already exist", not "is the table
// empty". Public registration can legitimately create members before anyone
// runs setup, and those accounts must not be mistaken for an initialised
// system — otherwise a remote visitor could close the setup window simply by
// registering.
//
// It returns ErrAdminUserNotFound when a super administrator already exists,
// which the service layer maps to "setup is no longer allowed".
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
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_admin_user WHERE `+superAdminPredicate).Scan(&existing); err != nil {
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

// CreateUser inserts an ordinary account.
//
// It is a plain INSERT and deliberately shares nothing with
// CreateFirstAdminUser: public registration must never be able to take the
// first-administrator path, so the two live in separate statements with
// separate guards. A username collision is reported as ErrAdminUsernameTaken
// rather than as a database error, so the HTTP layer can answer 409 without
// leaking constraint names.
func (p *Postgres) CreateUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error) {
	encoded, err := json.Marshal(roles)
	if err != nil {
		return model.AdminUser{}, err
	}
	q := `INSERT INTO gateway_admin_user (username, display_name, password_hash, roles)
VALUES ($1, $2, $3, $4::jsonb)
RETURNING ` + adminUserColumns
	user, err := scanAdminUser(p.Pool.QueryRow(ctx, q, username, displayName, passwordHash, encoded))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.AdminUser{}, ErrAdminUsernameTaken
		}
		return model.AdminUser{}, err
	}
	return user, nil
}

// AdminUserMutation is a partial change to an account. A nil field means
// "leave it alone", which is what lets the console send only the fields the
// operator actually touched.
type AdminUserMutation struct {
	DisplayName *string
	Roles       *[]string
	Status      *int
}

// AdminUserChangeSet reports what a mutation actually changed.
//
// It exists because "the request contained a roles field" and "the roles
// changed" are different facts. A no-op PATCH — the console resending the
// current values — must not bump auth_version, must not sign the user out and
// must not write an audit row claiming something happened. Only a comparison of
// the before and after states can tell those apart.
type AdminUserChangeSet struct {
	RolesChanged       bool
	StatusChanged      bool
	DisplayNameChanged bool

	OldRoles []string
	NewRoles []string

	OldStatus int
	NewStatus int

	OldDisplayName string
	NewDisplayName string
}

// AuthenticationChanged reports whether the change invalidates credentials.
//
// Role and status are what the access token's authority is derived from, so
// either one moving means every token issued before it must stop working.
// A display name is a label and has no bearing on authentication.
func (c AdminUserChangeSet) AuthenticationChanged() bool {
	return c.RolesChanged || c.StatusChanged
}

// AnythingChanged reports whether the row needs writing at all.
func (c AdminUserChangeSet) AnythingChanged() bool {
	return c.AuthenticationChanged() || c.DisplayNameChanged
}

// UpdateAdminUserAccount applies a partial account change in one transaction
// and reports what actually changed.
//
// Everything the change depends on is read and written inside a single
// transaction:
//
//   - the advisory lock serialises every mutation that could affect the
//     super-administrator invariant. A row lock is not enough on its own,
//     because two demotions can target two *different* rows: locking super1 and
//     super2 separately, each transaction would see the other still enabled and
//     both would commit, leaving no administrator at all.
//   - SELECT ... FOR UPDATE pins the target row so the before-state the
//     comparison is based on cannot move underneath it.
//   - the invariant is judged once, against the final state, rather than once
//     per field. A patch that demotes and disables in the same request is
//     evaluated as the single outcome it is.
//   - one UPDATE writes every field, bumps auth_version at most once and
//     leaves updated_at alone when nothing changed. A failure anywhere rolls
//     back all of it, so a rejected patch cannot leave the display name
//     updated while the role change was refused.
func (p *Postgres) UpdateAdminUserAccount(ctx context.Context, id string, mutation AdminUserMutation) (model.AdminUser, AdminUserChangeSet, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, adminUserGuardLockKey); err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}

	current, err := scanAdminUser(tx.QueryRow(ctx,
		`SELECT `+adminUserColumns+` FROM gateway_admin_user WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}

	next := current
	if mutation.Roles != nil {
		next.Roles = *mutation.Roles
	}
	if mutation.Status != nil {
		next.Status = *mutation.Status
	}
	if mutation.DisplayName != nil {
		next.DisplayName = *mutation.DisplayName
	}

	changes := AdminUserChangeSet{
		OldRoles:       current.Roles,
		NewRoles:       next.Roles,
		OldStatus:      current.Status,
		NewStatus:      next.Status,
		OldDisplayName: current.DisplayName,
		NewDisplayName: next.DisplayName,
	}
	changes.RolesChanged = !sameRoleSet(current.Roles, next.Roles)
	changes.StatusChanged = current.Status != next.Status
	changes.DisplayNameChanged = current.DisplayName != next.DisplayName

	// The invariant is judged on the outcome, not on the fields that were sent.
	if keepsEnabledSuperAdmin(current) && !keepsEnabledSuperAdmin(next) {
		var others int64
		if err := tx.QueryRow(ctx, `
SELECT COUNT(*) FROM gateway_admin_user
WHERE id <> $1 AND status = $2 AND `+superAdminPredicate, id, model.AdminStatusEnabled).Scan(&others); err != nil {
			return model.AdminUser{}, AdminUserChangeSet{}, err
		}
		if others == 0 {
			return model.AdminUser{}, AdminUserChangeSet{}, ErrLastSuperAdmin
		}
	}

	// A no-op is not a write. Touching updated_at would make the row look
	// edited when nothing was asked of it.
	if !changes.AnythingChanged() {
		if err := tx.Commit(ctx); err != nil {
			return model.AdminUser{}, AdminUserChangeSet{}, err
		}
		return current, changes, nil
	}

	encoded, err := json.Marshal(next.Roles)
	if err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}

	// One statement, so auth_version moves by exactly one however many fields
	// changed: a patch that alters roles, status and the display name together
	// is one logical change, not three.
	updated, err := scanAdminUser(tx.QueryRow(ctx, `
UPDATE gateway_admin_user
SET roles = $2::jsonb,
    status = $3,
    display_name = $4,
    auth_version = CASE WHEN $5 THEN auth_version + 1 ELSE auth_version END,
    updated_at = CASE WHEN $6 THEN NOW() ELSE updated_at END
WHERE id = $1
RETURNING `+adminUserColumns,
		id, encoded, next.Status, next.DisplayName, changes.AuthenticationChanged(), changes.AnythingChanged()))
	if err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.AdminUser{}, AdminUserChangeSet{}, err
	}
	return updated, changes, nil
}

// keepsEnabledSuperAdmin reports whether an account is currently a usable super
// administrator. A disabled one does not count: it could not sign in to
// administer anything.
func keepsEnabledSuperAdmin(u model.AdminUser) bool {
	return u.Status == model.AdminStatusEnabled && containsRole(u.Roles, "super_admin")
}

// sameRoleSet compares role lists as sets, so a reordered list is not mistaken
// for a change. The comparison is over the canonical form because the stored
// order is not part of the meaning of a role list.
func sameRoleSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, r := range a {
		set[r] = true
	}
	for _, r := range b {
		if !set[r] {
			return false
		}
	}
	return true
}

func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
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
