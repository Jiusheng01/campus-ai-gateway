package model

import "time"

// Small integers shared by the admin tables. They mirror the status encoding
// used elsewhere in the schema (1 = enabled, 2 = disabled).
const (
	AdminStatusEnabled  = 1
	AdminStatusDisabled = 2

	AdminSessionActive  = 1
	AdminSessionRevoked = 2
)

// AdminUser is a local control-plane account. Only a password hash is stored;
// the plaintext never reaches the database.
//
// The name is legacy. Since public registration was introduced this struct
// represents every account, not only administrators: a plain member is an
// AdminUser whose roles are ["member"]. The table name is kept as-is on purpose
// — renaming gateway_admin_user to gateway_user is a separate, mechanical
// change that would put the already-verified session layer at risk for no
// functional gain. See the technical-debt note in docs/ACCOUNTS.md.
type AdminUser struct {
	ID           string
	Username     string
	PasswordHash string
	DisplayName  string
	Roles        []string
	Status       int
	// AuthVersion invalidates every session and access token of this account
	// when it changes. Password changes, role changes and disabling bump it.
	AuthVersion int64
	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AdminUserStats backs the summary strip on the user management page. It is
// computed in one query so the counts cannot disagree with each other the way
// separate requests racing a concurrent signup would.
type AdminUserStats struct {
	Total    int64
	Members  int64
	Admins   int64
	Disabled int64
}

// AdminSession is one logged-in device. The refresh secret is only kept as a
// hash; the plaintext lives in the HttpOnly cookie held by the browser.
type AdminSession struct {
	SID     string
	UserID  string
	Version int64
	// UserAuthVersion is the account auth_version at issue time. When the
	// account version moves on, this session is stale even if it was never
	// revoked explicitly.
	UserAuthVersion int64
	Status          int
	RefreshHash     string
	// PreviousRefreshHash is the refresh hash this session had immediately
	// before the most recent rotation. Together with
	// PreviousRefreshValidUntil it lets a just-superseded token be accepted as
	// a benign concurrent refresh (two tabs racing) instead of being mistaken
	// for a leaked credential.
	PreviousRefreshHash       string
	PreviousRefreshValidUntil *time.Time
	LoginMethod               string
	IP                        string
	UserAgent                 string
	CreatedAt                 time.Time
	LastActiveAt              time.Time
	ExpiresAt                 time.Time
	RevokedAt                 *time.Time
	RevokeReason              string
}
