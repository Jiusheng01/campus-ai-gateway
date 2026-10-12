package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	adminSessionKeyPrefix = "gw:admin:session:"
	adminLoginKeyPrefix   = "gw:admin:login:"

	// MinAdminPasswordLength is enforced on first-run setup and on password
	// changes. It is a floor for a control plane that can mint API keys and
	// read every request log, so a short password is not acceptable.
	MinAdminPasswordLength = 10

	adminLoginBodyLimit = 4 << 10
)

// adminSession is what a session id resolves to. Only the account id is kept
// here; roles and status are re-read from PostgreSQL on every request so that
// disabling an account takes effect immediately rather than at session expiry.
type adminSession struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	// PasswordTag is a short fingerprint of the password hash the session was
	// issued with. Changing a password rewrites that hash, so every session
	// created beforehand stops matching and is rejected on its next request.
	PasswordTag string `json:"pt,omitempty"`
}

type adminCredentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	// SetupToken authorises the very first account creation. It carries the
	// existing ADMIN_TOKEN so a deployment can prove ownership once and then
	// never handle a shared token again.
	SetupToken string `json:"setup_token"`
}

type adminSessionResponse struct {
	Token     string   `json:"token"`
	User      string   `json:"user"`
	Roles     []string `json:"roles"`
	ExpiresAt string   `json:"expires_at"`
}

// dummyPasswordHash is compared against when the username does not exist, so
// that a missing account and a wrong password cost roughly the same wall time
// and cannot be told apart by timing.
var dummyPasswordHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("campus-gateway-timing-equaliser"), bcrypt.DefaultCost)
	if err != nil {
		return nil
	}
	return h
})

func (s *Server) sessionAuthEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(s.cfg.AdminAuthMode), "session")
}

func bearerValue(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

// ---------------------------------------------------------------- sessions

func (s *Server) createAdminSession(ctx context.Context, user model.AdminUser) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	sid := base64.RawURLEncoding.EncodeToString(buf)
	payload, err := json.Marshal(adminSession{UserID: user.ID, Username: user.Username, PasswordTag: passwordTag(user.PasswordHash)})
	if err != nil {
		return "", time.Time{}, err
	}
	ttl := s.cfg.AdminSessionTTL
	if err := s.redis.Client.Set(ctx, adminSessionKeyPrefix+sid, payload, ttl).Err(); err != nil {
		return "", time.Time{}, err
	}
	return sid, time.Now().Add(ttl), nil
}

func (s *Server) lookupAdminSession(ctx context.Context, sid string) (adminSession, bool) {
	if sid == "" {
		return adminSession{}, false
	}
	raw, err := s.redis.Client.Get(ctx, adminSessionKeyPrefix+sid).Bytes()
	if err != nil {
		return adminSession{}, false
	}
	var sess adminSession
	if json.Unmarshal(raw, &sess) != nil || sess.UserID == "" {
		return adminSession{}, false
	}
	return sess, true
}

func (s *Server) revokeAdminSession(ctx context.Context, sid string) {
	if sid == "" {
		return
	}
	_ = s.redis.Client.Del(ctx, adminSessionKeyPrefix+sid).Err()
}

// passwordTag is a non-reversible fingerprint used only to notice that a
// password changed. It is derived from a value that is already a bcrypt hash,
// is never shown to a client, and is useless as a credential.
func passwordTag(hash string) string {
	if hash == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hash))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// handleAdminPasswordChange rotates the password of the signed-in account and
// invalidates every session issued with the previous password.
func (s *Server) handleAdminPasswordChange(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.sessionAuthEnabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "session_auth_disabled",
			"Password login is disabled; set ADMIN_AUTH_MODE=session to enable it", reqID)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized", "需要登录后才能修改密码", reqID)
		return
	}
	// The shared ADMIN_TOKEN break-glass principal has no database row.
	if principal.User == "admin_token" {
		httpx.Error(w, http.StatusConflict, "not_a_local_account",
			"当前会话由 ADMIN_TOKEN 提供；请改用账号密码登录后再修改密码", reqID)
		return
	}

	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, adminLoginBodyLimit)).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body could not be parsed", reqID)
		return
	}
	_ = r.Body.Close()

	if err := validateAdminPassword(body.NewPassword); err != nil {
		httpx.Error(w, http.StatusBadRequest, "weak_password", err.Error(), reqID)
		return
	}
	if body.NewPassword == body.OldPassword {
		httpx.Error(w, http.StatusBadRequest, "same_password", "新密码不能与原密码相同", reqID)
		return
	}

	user, err := s.pg.FindAdminUserByUsername(r.Context(), principal.User)
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "database_error", "Unable to load the account", reqID)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.OldPassword)) != nil {
		s.auditAdminAuth(r, user.Username, "PASSWORD_CHANGE_FAILED", http.StatusUnauthorized)
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "原密码不正确", reqID)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Unable to hash the password", reqID)
		return
	}
	if err := s.pg.UpdateAdminUserPassword(r.Context(), user.ID, string(hash)); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "无法更新密码", reqID)
		return
	}

	// Sessions issued with the old password now fail their fingerprint check,
	// so every device is forced to sign in again with the new password.
	s.revokeAdminSession(r.Context(), bearerValue(r))
	s.auditAdminAuth(r, user.Username, "PASSWORD_CHANGED", http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------ brute force

// allowAdminLoginAttempt throttles password guessing per source IP. It fails
// open on a Redis error: locking every administrator out of the control plane
// because of a cache hiccup is worse than briefly weakening the throttle.
func (s *Server) allowAdminLoginAttempt(ctx context.Context, ip string) bool {
	if s.cfg.AdminLoginMaxAttempts <= 0 {
		return true
	}
	key := adminLoginKeyPrefix + ip
	n, err := s.redis.Client.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		_ = s.redis.Client.Expire(ctx, key, s.cfg.AdminLoginWindow).Err()
	}
	return n <= s.cfg.AdminLoginMaxAttempts
}

func (s *Server) resetAdminLoginAttempts(ctx context.Context, ip string) {
	_ = s.redis.Client.Del(ctx, adminLoginKeyPrefix+ip).Err()
}

// -------------------------------------------------------------- handlers

// handleAdminAuthConfig is intentionally unauthenticated: the login page needs
// to know whether password login is available and whether the deployment still
// has no administrator account.
func (s *Server) handleAdminAuthConfig(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(s.cfg.AdminAuthMode))
	out := map[string]any{
		"auth_mode":       mode,
		"session_enabled": mode == "session",
		"needs_setup":     false,
		"min_password_length": MinAdminPasswordLength,
	}
	if mode == "session" {
		n, err := s.pg.CountAdminUsers(r.Context())
		if err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "database_error", "Unable to read administrator accounts", reqID)
			return
		}
		out["needs_setup"] = n == 0
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.sessionAuthEnabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "session_auth_disabled",
			"Password login is disabled; set ADMIN_AUTH_MODE=session to enable it", reqID)
		return
	}

	var body adminCredentials
	if err := json.NewDecoder(io.LimitReader(r.Body, adminLoginBodyLimit)).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body could not be parsed", reqID)
		return
	}
	_ = r.Body.Close()

	username := strings.TrimSpace(body.Username)
	ip := clientIP(r)

	if !s.allowAdminLoginAttempt(r.Context(), ip) {
		s.recordSecurityEvent(r, model.SecurityEvent{
			RequestID: reqID, EventType: "admin_login_throttled", ClientIP: ip,
			Detail: "too many failed administrator login attempts",
		})
		httpx.Error(w, http.StatusTooManyRequests, "too_many_attempts",
			"登录失败次数过多，请稍后再试", reqID)
		return
	}

	user, err := s.pg.FindAdminUserByUsername(r.Context(), username)
	if err != nil {
		if !errors.Is(err, storage.ErrAdminUserNotFound) {
			httpx.Error(w, http.StatusServiceUnavailable, "database_error", "Unable to verify credentials", reqID)
			return
		}
		if h := dummyPasswordHash(); h != nil {
			_ = bcrypt.CompareHashAndPassword(h, []byte(body.Password))
		}
		s.auditAdminAuth(r, username, "LOGIN_FAILED", http.StatusUnauthorized)
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码不正确", reqID)
		return
	}
	if user.Status != 1 {
		s.auditAdminAuth(r, user.Username, "LOGIN_DISABLED", http.StatusForbidden)
		httpx.Error(w, http.StatusForbidden, "account_disabled", "该账号已被停用", reqID)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		s.auditAdminAuth(r, user.Username, "LOGIN_FAILED", http.StatusUnauthorized)
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码不正确", reqID)
		return
	}

	sid, expires, err := s.createAdminSession(r.Context(), user)
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "session_error", "无法创建登录会话", reqID)
		return
	}
	s.resetAdminLoginAttempts(r.Context(), ip)
	s.pg.TouchAdminUserLogin(r.Context(), user.ID)
	s.auditAdminAuth(r, user.Username, "LOGIN", http.StatusOK)

	writeJSON(w, http.StatusOK, adminSessionResponse{
		Token:     sid,
		User:      user.Username,
		Roles:     user.Roles,
		ExpiresAt: expires.UTC().Format(time.RFC3339),
	})
}

// handleAdminLogout stays unauthenticated on purpose: a session that already
// expired must still be able to clear itself. Deleting a session requires
// knowing its id, so there is nothing to gain by calling this anonymously.
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	s.revokeAdminSession(r.Context(), bearerValue(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminSetup creates the very first administrator account. It refuses to
// do anything once an account exists, so it cannot be used to add a second
// backdoor later.
func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.sessionAuthEnabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "session_auth_disabled",
			"Password login is disabled; set ADMIN_AUTH_MODE=session to enable it", reqID)
		return
	}

	existing, err := s.pg.CountAdminUsers(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "database_error", "Unable to read administrator accounts", reqID)
		return
	}
	if existing > 0 {
		httpx.Error(w, http.StatusConflict, "setup_already_done", "管理员账号已存在，请直接登录", reqID)
		return
	}

	var body adminCredentials
	if err := json.NewDecoder(io.LimitReader(r.Body, adminLoginBodyLimit)).Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body could not be parsed", reqID)
		return
	}
	_ = r.Body.Close()

	// When a shared ADMIN_TOKEN is configured it acts as the one-time proof of
	// ownership for first-run setup. Without this check anyone who can reach the
	// port could claim the gateway before the operator does.
	if s.cfg.AdminToken != "" {
		got := strings.TrimSpace(body.SetupToken)
		if got == "" {
			got = strings.TrimSpace(r.Header.Get("X-Admin-Setup-Token"))
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminToken)) != 1 {
			s.recordSecurityEvent(r, model.SecurityEvent{
				RequestID: reqID, EventType: "admin_setup_rejected", ClientIP: clientIP(r),
				Detail: "first-run setup presented an invalid setup token",
			})
			httpx.Error(w, http.StatusForbidden, "setup_token_required",
				"首次初始化需要提供管理员令牌（ADMIN_TOKEN）", reqID)
			return
		}
	}

	username := strings.TrimSpace(body.Username)
	if username == "" {
		username = "admin"
	}
	if err := validateAdminPassword(body.Password); err != nil {
		httpx.Error(w, http.StatusBadRequest, "weak_password", err.Error(), reqID)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Unable to hash the password", reqID)
		return
	}
	display := strings.TrimSpace(body.DisplayName)
	if display == "" {
		display = username
	}

	user, err := s.pg.CreateFirstAdminUser(r.Context(), username, display, string(hash), []string{"super_admin"})
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			httpx.Error(w, http.StatusConflict, "setup_already_done", "管理员账号已存在，请直接登录", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "无法创建管理员账号", reqID)
		return
	}

	sid, expires, err := s.createAdminSession(r.Context(), user)
	if err != nil {
		// The account exists now; the operator can simply log in instead.
		httpx.Error(w, http.StatusServiceUnavailable, "session_error", "账号已创建，请重新登录", reqID)
		return
	}
	s.auditAdminAuth(r, user.Username, "SETUP", http.StatusOK)
	s.pg.TouchAdminUserLogin(r.Context(), user.ID)

	writeJSON(w, http.StatusCreated, adminSessionResponse{
		Token:     sid,
		User:      user.Username,
		Roles:     user.Roles,
		ExpiresAt: expires.UTC().Format(time.RFC3339),
	})
}

func validateAdminPassword(password string) error {
	if len([]rune(password)) < MinAdminPasswordLength {
		return errors.New("密码长度至少需要 10 个字符")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("密码不能全为空白字符")
	}
	return nil
}

func (s *Server) auditAdminAuth(r *http.Request, actor, action string, status int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.pg.InsertAdminAudit(ctx, model.AdminAuditLog{
		RequestID:  r.Header.Get("X-Request-Id"),
		Actor:      actor,
		Action:     action,
		Resource:   r.URL.Path,
		StatusCode: status,
		ClientIP:   clientIP(r),
	})
}

// bootstrapAdminUser creates the first account from environment variables so a
// scripted deployment never needs to drive the setup wizard. It is a no-op once
// any account exists.
func (s *Server) bootstrapAdminUser(ctx context.Context) error {
	if !s.sessionAuthEnabled() || s.cfg.AdminBootstrapPassword == "" {
		return nil
	}
	if err := validateAdminPassword(s.cfg.AdminBootstrapPassword); err != nil {
		return err
	}
	n, err := s.pg.CountAdminUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminBootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(s.cfg.AdminBootstrapUsername)
	if username == "" {
		username = "admin"
	}
	_, err = s.pg.CreateFirstAdminUser(ctx, username, username, string(hash), []string{"super_admin"})
	return err
}
