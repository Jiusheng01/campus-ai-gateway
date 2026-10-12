package model

import "time"

// AdminUser is a local control-plane account used by the username/password
// login flow (ADMIN_AUTH_MODE=session). The password hash is deliberately not
// serialised so it can never leak through the management API.
type AdminUser struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name"`
	Roles        []string   `json:"roles"`
	Status       int        `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// PasswordHash is bcrypt output. Tagged "-" so it stays inside the process.
	PasswordHash string `json:"-"`
}
