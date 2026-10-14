package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// bcryptHash produces a legacy-format hash so the transparent-upgrade path can
// be exercised without keeping a hard-coded hash in the repository.
func bcryptHash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return string(h), err
}

// --- fakes -----------------------------------------------------------------

// fakeAdminStore is an in-memory adminStore. It reproduces the two behaviours
// the service relies on for correctness: the NOT EXISTS guard on first-run
// setup, and the compare-and-swap on refresh rotation.
type fakeAdminStore struct {
	users    map[string]model.AdminUser
	names    map[string]string // lower(username) -> id
	sessions map[string]model.AdminSession
	seq      int

	// rotateCalls records (sid, oldHash, newHash) for rotation assertions.
	rotateCalls []string

	// staleRead, when set, is returned by the next FindAdminSessionByID call
	// instead of the stored row, then cleared. It reproduces the interleaving
	// where a second request read the session before the first rotation landed.
	staleRead *model.AdminSession
}

func newFakeAdminStore() *fakeAdminStore {
	return &fakeAdminStore{
		users:    map[string]model.AdminUser{},
		names:    map[string]string{},
		sessions: map[string]model.AdminSession{},
	}
}

func (f *fakeAdminStore) CountSuperAdmins(context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if hasSuperAdmin(u.Roles) {
			n++
		}
	}
	return n, nil
}

// enabledSuperAdmins counts the accounts that keep the console administrable.
// The real store enforces the same invariant inside a transaction; the fake
// mirrors it so the service-level tests exercise the same rule.
func (f *fakeAdminStore) enabledSuperAdmins() int64 {
	var n int64
	for _, u := range f.users {
		if u.Status == model.AdminStatusEnabled && hasSuperAdmin(u.Roles) {
			n++
		}
	}
	return n
}

func (f *fakeAdminStore) AdminUserStats(context.Context) (model.AdminUserStats, error) {
	var out model.AdminUserStats
	for _, u := range f.users {
		out.Total++
		if hasManagementRole(u.Roles) {
			out.Admins++
		} else {
			out.Members++
		}
		if u.Status != model.AdminStatusEnabled {
			out.Disabled++
		}
	}
	return out, nil
}

func (f *fakeAdminStore) ListAdminUsers(_ context.Context, limit int) ([]model.AdminUser, error) {
	out := make([]model.AdminUser, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeAdminStore) FindAdminUserByUsername(_ context.Context, username string) (model.AdminUser, error) {
	id, ok := f.names[strings.ToLower(strings.TrimSpace(username))]
	if !ok {
		return model.AdminUser{}, storage.ErrAdminUserNotFound
	}
	return f.users[id], nil
}

func (f *fakeAdminStore) FindAdminUserByID(_ context.Context, id string) (model.AdminUser, error) {
	u, ok := f.users[id]
	if !ok {
		return model.AdminUser{}, storage.ErrAdminUserNotFound
	}
	return u, nil
}

func (f *fakeAdminStore) CreateFirstAdminUser(_ context.Context, username, displayName, hash string, roles []string) (model.AdminUser, error) {
	// Mirrors the storage guard: setup is closed by an existing super
	// administrator, not by any account at all. A registered member must not
	// be able to close the setup window.
	if n, _ := f.CountSuperAdmins(context.Background()); n > 0 {
		return model.AdminUser{}, storage.ErrAdminUserNotFound
	}
	if _, taken := f.names[strings.ToLower(strings.TrimSpace(username))]; taken {
		return model.AdminUser{}, storage.ErrAdminUsernameTaken
	}
	f.seq++
	id := uuid.NewString()
	now := time.Now().UTC()
	u := model.AdminUser{
		ID: id, Username: username, DisplayName: displayName, PasswordHash: hash,
		Roles: roles, Status: model.AdminStatusEnabled, AuthVersion: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	f.users[id] = u
	f.names[strings.ToLower(username)] = id
	return u, nil
}

func (f *fakeAdminStore) CreateUser(_ context.Context, username, displayName, hash string, roles []string) (model.AdminUser, error) {
	if _, taken := f.names[strings.ToLower(strings.TrimSpace(username))]; taken {
		return model.AdminUser{}, storage.ErrAdminUsernameTaken
	}
	f.seq++
	id := uuid.NewString()
	now := time.Now().UTC()
	u := model.AdminUser{
		ID: id, Username: username, DisplayName: displayName, PasswordHash: hash,
		Roles: roles, Status: model.AdminStatusEnabled, AuthVersion: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	f.users[id] = u
	f.names[strings.ToLower(username)] = id
	return u, nil
}

func (f *fakeAdminStore) UpdateAdminUserRoles(_ context.Context, id string, roles []string) error {
	u, ok := f.users[id]
	if !ok {
		return storage.ErrAdminUserNotFound
	}
	// The invariant is checked against the state after the change, exactly as
	// the guarded storage transaction does.
	if u.Status == model.AdminStatusEnabled && hasSuperAdmin(u.Roles) && !hasSuperAdmin(roles) && f.enabledSuperAdmins() <= 1 {
		return storage.ErrLastSuperAdmin
	}
	u.Roles = roles
	u.AuthVersion++
	f.users[id] = u
	return nil
}

func (f *fakeAdminStore) SetAdminUserStatus(_ context.Context, id string, status int) error {
	u, ok := f.users[id]
	if !ok {
		return storage.ErrAdminUserNotFound
	}
	if status != model.AdminStatusEnabled && hasSuperAdmin(u.Roles) && u.Status == model.AdminStatusEnabled && f.enabledSuperAdmins() <= 1 {
		return storage.ErrLastSuperAdmin
	}
	u.Status = status
	u.AuthVersion++
	f.users[id] = u
	return nil
}

func (f *fakeAdminStore) UpdateAdminUserDisplayName(_ context.Context, id, displayName string) error {
	u, ok := f.users[id]
	if !ok {
		return storage.ErrAdminUserNotFound
	}
	u.DisplayName = displayName
	// Deliberately does not bump AuthVersion: a label change must not sign
	// anyone out.
	f.users[id] = u
	return nil
}

func (f *fakeAdminStore) UpdateAdminUserPassword(_ context.Context, id, hash string) error {
	u, ok := f.users[id]
	if !ok {
		return storage.ErrAdminUserNotFound
	}
	u.PasswordHash = hash
	u.AuthVersion++
	f.users[id] = u
	return nil
}

func (f *fakeAdminStore) RehashAdminUserPassword(_ context.Context, id, hash string) error {
	u, ok := f.users[id]
	if !ok {
		return storage.ErrAdminUserNotFound
	}
	u.PasswordHash = hash
	// Deliberately does not bump AuthVersion.
	f.users[id] = u
	return nil
}

func (f *fakeAdminStore) TouchAdminUserLogin(_ context.Context, id string) {
	u, ok := f.users[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	u.LastLoginAt = &now
	f.users[id] = u
}

func (f *fakeAdminStore) CreateAdminSession(_ context.Context, s model.AdminSession) (model.AdminSession, error) {
	if _, exists := f.sessions[s.SID]; exists {
		return model.AdminSession{}, errors.New("duplicate session id")
	}
	now := time.Now().UTC()
	s.Status = model.AdminSessionActive
	s.Version = 1
	s.CreatedAt = now
	s.LastActiveAt = now
	f.sessions[s.SID] = s
	return s, nil
}

func (f *fakeAdminStore) FindAdminSessionByID(_ context.Context, sid string) (model.AdminSession, error) {
	if f.staleRead != nil {
		s := *f.staleRead
		f.staleRead = nil
		return s, nil
	}
	s, ok := f.sessions[sid]
	if !ok {
		return model.AdminSession{}, storage.ErrAdminSessionNotFound
	}
	return s, nil
}

func (f *fakeAdminStore) RotateAdminSessionRefresh(_ context.Context, sid, oldHash, newHash string, graceUntil time.Time) (bool, error) {
	s, ok := f.sessions[sid]
	if !ok {
		return false, nil
	}
	if s.RefreshHash != oldHash || s.Status != model.AdminSessionActive || s.RevokedAt != nil {
		return false, nil
	}
	f.rotateCalls = append(f.rotateCalls, sid)
	// Mirror the real statement: park the superseded hash in the previous slot
	// so a benign concurrent refresh can still be recognised.
	s.PreviousRefreshHash = oldHash
	until := graceUntil
	s.PreviousRefreshValidUntil = &until
	s.RefreshHash = newHash
	f.sessions[sid] = s
	return true, nil
}

func (f *fakeAdminStore) TouchAdminSession(_ context.Context, sid string) {
	s, ok := f.sessions[sid]
	if !ok {
		return
	}
	s.LastActiveAt = time.Now().UTC()
	f.sessions[sid] = s
}

func (f *fakeAdminStore) RevokeAdminSession(_ context.Context, sid, reason string) error {
	s, ok := f.sessions[sid]
	if !ok {
		return nil
	}
	if s.RevokedAt == nil {
		now := time.Now().UTC()
		s.RevokedAt = &now
		s.RevokeReason = reason
		s.Status = model.AdminSessionRevoked
		f.sessions[sid] = s
	}
	return nil
}

func (f *fakeAdminStore) RevokeSessionsForUser(_ context.Context, userID, reason string) error {
	for sid, s := range f.sessions {
		if s.UserID != userID || s.RevokedAt != nil {
			continue
		}
		now := time.Now().UTC()
		s.RevokedAt = &now
		s.RevokeReason = reason
		s.Status = model.AdminSessionRevoked
		f.sessions[sid] = s
	}
	return nil
}

func (f *fakeAdminStore) ListAdminSessions(_ context.Context, userID string) ([]model.AdminSession, error) {
	var out []model.AdminSession
	for _, s := range f.sessions {
		if s.UserID == userID && s.Status == model.AdminSessionActive && s.RevokedAt == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// liveSessions counts non-revoked sessions, used by assertions.
func (f *fakeAdminStore) liveSessions() int {
	n := 0
	for _, s := range f.sessions {
		if s.Status == model.AdminSessionActive && s.RevokedAt == nil {
			n++
		}
	}
	return n
}

// fakeLimiter is an in-memory adminLoginLimiter.
type fakeLimiter struct {
	max    int
	counts map[string]int
	broken bool
}

func newFakeLimiter(max int) *fakeLimiter {
	return &fakeLimiter{max: max, counts: map[string]int{}}
}

func (l *fakeLimiter) AllowAdminLogin(_ context.Context, ip, username string) (bool, time.Duration, error) {
	if l.broken {
		return true, 0, errors.New("limiter unavailable")
	}
	if l.max <= 0 {
		return true, 0, nil
	}
	if l.counts[ip+"/"+username] >= l.max {
		return false, 5 * time.Minute, nil
	}
	return true, 0, nil
}

func (l *fakeLimiter) RecordAdminLoginFailure(_ context.Context, ip, username string) error {
	l.counts[ip+"/"+username]++
	return nil
}

func (l *fakeLimiter) ResetAdminLogin(_ context.Context, ip, username string) error {
	delete(l.counts, ip+"/"+username)
	return nil
}

// fakeRegistrationLimiter is an in-memory registrationLimiter. broken models a
// Redis outage, which registration must fail closed on.
type fakeRegistrationLimiter struct {
	max     int64
	window  time.Duration
	counts  map[string]int64
	broken  bool
	records int64
}

func newFakeRegistrationLimiter(max int64) *fakeRegistrationLimiter {
	return &fakeRegistrationLimiter{max: max, window: time.Hour, counts: map[string]int64{}}
}

func (l *fakeRegistrationLimiter) AllowRegistration(_ context.Context, ip string) (bool, time.Duration, error) {
	if l.broken {
		return false, 0, errors.New("limiter unavailable")
	}
	if l.max <= 0 {
		return true, 0, nil
	}
	if l.counts[ip] >= l.max {
		return false, l.window, nil
	}
	return true, 0, nil
}

func (l *fakeRegistrationLimiter) RecordRegistration(_ context.Context, ip string) error {
	if l.broken {
		return errors.New("limiter unavailable")
	}
	l.counts[ip]++
	l.records++
	return nil
}

// --- helpers ---------------------------------------------------------------

func testConfig() Config {
	return Config{
		AdminAuthMode:             "session",
		AdminAccessTTL:            15 * time.Minute,
		AdminRefreshTTL:           30 * 24 * time.Hour,
		AdminLoginMaxAttempts:     8,
		AdminLoginWindow:          15 * time.Minute,
		AdminSetupLocalOnly:       true,
		AdminRefreshReplayGrace:   30 * time.Second,
		PublicRegistrationEnabled: true,
		PublicRegisterMaxAttempts: 5,
		PublicRegisterWindow:      time.Hour,
	}
}

func newTestService(t *testing.T) (*adminAuthService, *fakeAdminStore, *fakeLimiter) {
	t.Helper()
	store := newFakeAdminStore()
	limiter := newFakeLimiter(8)
	svc := newAdminAuthService(store, limiter, []byte("unit-test-signing-secret"), testConfig()).
		withRegistrationLimiter(newFakeRegistrationLimiter(5))
	return svc, store, limiter
}

// seedAdmin inserts an account directly, bypassing setup.
func seedAdmin(t *testing.T, store *fakeAdminStore, username, password string) model.AdminUser {
	t.Helper()
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateFirstAdminUser(context.Background(), username, "Test Admin", hash, []string{"super_admin"})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return u
}

// --- setup -----------------------------------------------------------------

func TestSetupCreatesFirstAdministrator(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()

	needs, err := svc.needsSetup(ctx)
	if err != nil || !needs {
		t.Fatalf("expected setup to be required: needs=%v err=%v", needs, err)
	}

	user, err := svc.setup(ctx, "campus-admin", "能小智管理员", "Campus#2026Gate")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if user.Username != "campus-admin" {
		t.Fatalf("username mismatch: %q", user.Username)
	}
	if len(user.Roles) != 1 || user.Roles[0] != "super_admin" {
		t.Fatalf("first administrator must be super_admin, got %v", user.Roles)
	}
	if user.AuthVersion != 1 {
		t.Fatalf("auth_version must start at 1, got %d", user.AuthVersion)
	}
	if !security.IsArgon2idHash(user.PasswordHash) {
		t.Fatalf("password must be stored as argon2id, got %q", user.PasswordHash)
	}
	if store.liveSessions() != 0 {
		t.Fatal("setup must not create a session")
	}

	needs, err = svc.needsSetup(ctx)
	if err != nil || needs {
		t.Fatalf("setup must no longer be required: needs=%v err=%v", needs, err)
	}
}

func TestSetupRejectedWhenAdministratorExists(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "existing", "Existing#2026Pass")

	if _, err := svc.setup(context.Background(), "second", "", "Second#2026Pass"); !errors.Is(err, ErrAdminSetupNotAllowed) {
		t.Fatalf("expected ErrAdminSetupNotAllowed, got %v", err)
	}
}

func TestSetupRejectsWeakPassword(t *testing.T) {
	svc, _, _ := newTestService(t)
	cases := []string{"short1!", "alllowercase", "1234567890123", "campus-admin"}
	for _, pw := range cases {
		if _, err := svc.setup(context.Background(), "campus-admin", "", pw); !errors.Is(err, ErrAdminPasswordWeak) {
			t.Fatalf("password %q must be rejected, got %v", pw, err)
		}
	}
}

func TestSetupRejectsBlankUsername(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.setup(context.Background(), "   ", "", "Campus#2026Gate"); !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("expected ErrAdminBadRequest, got %v", err)
	}
}

// --- login -----------------------------------------------------------------

func TestLoginSuccessIssuesTokensAndSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("login must return both tokens")
	}
	if !strings.HasPrefix(tokens.RefreshToken, tokens.Session.SID+".") {
		t.Fatalf("refresh token must embed the session id: %q", tokens.RefreshToken)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("expected exactly one live session, got %d", store.liveSessions())
	}
	stored := store.sessions[tokens.Session.SID]
	if stored.RefreshHash != security.HashAdminRefreshToken(tokens.RefreshToken) {
		t.Fatal("only the refresh hash must be stored")
	}
	if strings.Contains(stored.RefreshHash, tokens.RefreshToken) {
		t.Fatal("the stored hash must not contain the plaintext token")
	}
	if stored.IP != "10.0.0.5" {
		t.Fatalf("session must record the client ip, got %q", stored.IP)
	}
	if store.users[tokens.User.ID].LastLoginAt == nil {
		t.Fatal("last_login_at must be recorded")
	}
}

func TestLoginIsCaseInsensitiveOnUsername(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "Campus-Admin", "Campus#2026Gate")

	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", ""); err != nil {
		t.Fatalf("username lookup must be case-insensitive: %v", err)
	}
}

func TestLoginWrongPasswordIsIndistinguishable(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	_, errWrong := svc.login(context.Background(), "campus-admin", "Wrong#2026Gate", "10.0.0.5", "")
	_, errMissing := svc.login(context.Background(), "no-such-user", "Wrong#2026Gate", "10.0.0.5", "")
	if !errors.Is(errWrong, ErrAdminCredentialsInvalid) {
		t.Fatalf("wrong password: expected ErrAdminCredentialsInvalid, got %v", errWrong)
	}
	if !errors.Is(errMissing, ErrAdminCredentialsInvalid) {
		t.Fatalf("unknown user: expected ErrAdminCredentialsInvalid, got %v", errMissing)
	}
	if errWrong.Error() != errMissing.Error() {
		t.Fatal("the two failures must be indistinguishable to the caller")
	}
}

func TestLoginRejectsDisabledAccount(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	u.Status = model.AdminStatusDisabled
	store.users[u.ID] = u

	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", ""); !errors.Is(err, ErrAdminUserDisabled) {
		t.Fatalf("expected ErrAdminUserDisabled, got %v", err)
	}
}

func TestLoginThrottledAfterMaxAttempts(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	for i := 0; i < 8; i++ {
		if _, err := svc.login(context.Background(), "campus-admin", "nope-nope-nope", "10.0.0.9", ""); !errors.Is(err, ErrAdminCredentialsInvalid) {
			t.Fatalf("attempt %d: expected ErrAdminCredentialsInvalid, got %v", i, err)
		}
	}
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.9", ""); !errors.Is(err, ErrAdminRateLimited) {
		t.Fatalf("expected ErrAdminRateLimited once the window is full, got %v", err)
	}
	// A different IP must be unaffected.
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.10", ""); err != nil {
		t.Fatalf("a different client must not be throttled: %v", err)
	}
}

func TestLoginResetsThrottleCounterOnSuccess(t *testing.T) {
	svc, store, limiter := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	for i := 0; i < 3; i++ {
		_, _ = svc.login(context.Background(), "campus-admin", "bad", "10.0.0.9", "")
	}
	if limiter.counts["10.0.0.9/campus-admin"] != 3 {
		t.Fatalf("expected 3 recorded failures, got %d", limiter.counts["10.0.0.9/campus-admin"])
	}
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.9", ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	if limiter.counts["10.0.0.9/campus-admin"] != 0 {
		t.Fatal("a successful login must clear the failure counter")
	}
}

func TestLoginUpgradesBcryptHashWithoutBumpingAuthVersion(t *testing.T) {
	svc, store, _ := newTestService(t)
	user := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	// Simulate a legacy row written by an older release.
	legacy, err := bcryptHash("Campus#2026Gate")
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	user.PasswordHash = legacy
	store.users[user.ID] = user

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login with a bcrypt hash must succeed: %v", err)
	}
	after := store.users[user.ID]
	if !security.IsArgon2idHash(after.PasswordHash) {
		t.Fatalf("the hash must be upgraded to argon2id, got %q", after.PasswordHash)
	}
	if after.AuthVersion != user.AuthVersion {
		t.Fatalf("transparent upgrade must not bump auth_version: %d -> %d", user.AuthVersion, after.AuthVersion)
	}
	// The session must still work after the upgrade.
	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); err != nil {
		t.Fatalf("the freshly issued access token must verify: %v", err)
	}
}

func TestLoginFailsOnMalformedStoredHash(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	u.PasswordHash = "not-a-hash"
	store.users[u.ID] = u

	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", ""); !errors.Is(err, ErrAdminInternal) {
		t.Fatalf("expected ErrAdminInternal for a corrupt hash, got %v", err)
	}
}

// --- refresh ---------------------------------------------------------------

func TestRefreshRotatesToken(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	first, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	second, err := svc.refresh(context.Background(), first.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh must rotate the token")
	}
	if second.Session.SID != first.Session.SID {
		t.Fatal("rotation must keep the same session id")
	}
	if store.sessions[first.Session.SID].RefreshHash != security.HashAdminRefreshToken(second.RefreshToken) {
		t.Fatal("the stored hash must match the newly issued token")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("rotation must not create a second session, got %d", store.liveSessions())
	}
	if second.AccessToken == "" {
		t.Fatal("refresh must return a new access token")
	}
}

// TestRefreshReuseWithinGraceIsBenign covers the multi-tab regression: two
// browser tabs refresh at the same moment, and the loser of the database race
// must not be mistaken for a leaked credential.
func TestRefreshReuseWithinGraceIsBenign(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	s1, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	first, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// The second tab presents the same superseded token inside the grace window.
	second, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("a concurrent refresh inside the grace window must succeed, got %v", err)
	}
	// Rotation is deterministic, so both tabs converge on one cookie value.
	// Otherwise the later response would clobber the earlier one.
	if second.RefreshToken != first.RefreshToken {
		t.Fatalf("concurrent refreshes must converge on one successor:\n first=%q\nsecond=%q",
			first.RefreshToken, second.RefreshToken)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a benign race must not revoke anything, %d live sessions", store.liveSessions())
	}
	if reason := store.sessions[s1.Session.SID].RevokeReason; reason != "" {
		t.Fatalf("a benign race must not record a revoke reason, got %q", reason)
	}
	if _, err := svc.refresh(context.Background(), second.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("the successor must stay usable: %v", err)
	}
}

// TestRefreshReplayOutsideGraceRevokesEverySession covers genuine reuse: the
// superseded token reappears after the benign-concurrency window has closed.
func TestRefreshReplayOutsideGraceRevokesEverySession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	s1, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	_, _ = svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")
	if store.liveSessions() != 2 {
		t.Fatalf("expected 2 live sessions, got %d", store.liveSessions())
	}

	base := time.Now()
	svc.now = func() time.Time { return base }
	rotated, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Move past the grace window before the superseded token reappears.
	svc.now = func() time.Time { return base.Add(testConfig().AdminRefreshReplayGrace + time.Second) }

	if _, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("expected ErrAdminRefreshReplay on reuse outside the grace window, got %v", err)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("replay must revoke every session of the account, %d still live", store.liveSessions())
	}
	if store.sessions[s1.Session.SID].RevokeReason != revokeReasonReuse {
		t.Fatalf("expected reason %q, got %q", revokeReasonReuse, store.sessions[s1.Session.SID].RevokeReason)
	}
	// The token issued by the legitimate rotation is dead too.
	if _, err := svc.refresh(context.Background(), rotated.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminSessionRevoked) {
		t.Fatalf("the whole family must be revoked, got %v", err)
	}
}

func TestRefreshRejectsExpiredSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	s := store.sessions[tokens.Session.SID]
	s.ExpiresAt = time.Now().Add(-time.Minute)
	store.sessions[s.SID] = s

	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminSessionExpired) {
		t.Fatalf("expected ErrAdminSessionExpired, got %v", err)
	}
}

func TestRefreshRejectsStaleSessionAfterPasswordChange(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	u := store.users[tokens.User.ID]
	u.AuthVersion++
	store.users[u.ID] = u

	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminSessionExpired) {
		t.Fatalf("expected ErrAdminSessionExpired, got %v", err)
	}
	if store.sessions[tokens.Session.SID].RevokeReason != revokeReasonStale {
		t.Fatal("a stale session must be revoked with the auth_version reason")
	}
}

func TestRefreshRejectsUnknownSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.refresh(context.Background(), uuid.NewString()+".secret", "10.0.0.5", ""); !errors.Is(err, ErrAdminSessionNotFound) {
		t.Fatalf("expected ErrAdminSessionNotFound, got %v", err)
	}
}

func TestRefreshRejectsMalformedToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	for _, tok := range []string{"", "no-dot", ".onlysecret", "sidonly."} {
		if _, err := svc.refresh(context.Background(), tok, "10.0.0.5", ""); !errors.Is(err, ErrAdminUnauthenticated) {
			t.Fatalf("%q: expected ErrAdminUnauthenticated, got %v", tok, err)
		}
	}
}

// --- access token verification --------------------------------------------

func TestAuthenticateAcceptsValidAccessToken(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	p, err := svc.authenticate(context.Background(), tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if p.UserID != tokens.User.ID || p.SessionID != tokens.Session.SID {
		t.Fatalf("principal mismatch: %+v", p)
	}
	if p.Username != "campus-admin" || !p.Roles["super_admin"] {
		t.Fatalf("principal must carry the account roles: %+v", p)
	}
	if p.AuthMode != "session" {
		t.Fatalf("expected auth mode session, got %q", p.AuthMode)
	}
}

func TestAuthenticateRejectsExpiredAccessToken(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	session := store.sessions[tokens.Session.SID]

	// JWT expiry is evaluated against the real clock inside the security
	// package, so the expired token is minted directly rather than by moving
	// the service clock.
	expired, err := security.SignAdminAccessToken(svc.secret, security.AdminAccessClaims{
		Subject:         tokens.User.ID,
		SessionID:       session.SID,
		UserAuthVersion: tokens.User.AuthVersion,
		SessionVersion:  session.Version,
		IssuedAt:        time.Now().Add(-time.Hour),
		NotBefore:       time.Now().Add(-time.Hour),
		ExpiresAt:       time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := svc.authenticate(context.Background(), expired); !errors.Is(err, ErrAdminSessionExpired) {
		t.Fatalf("expected ErrAdminSessionExpired, got %v", err)
	}
}

func TestAuthenticateRejectsRevokedSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	if err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminSessionRevoked) {
		t.Fatalf("expected ErrAdminSessionRevoked, got %v", err)
	}
}

func TestAuthenticateRejectsBumpedSessionVersion(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	s := store.sessions[tokens.Session.SID]
	s.Version++
	store.sessions[s.SID] = s

	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminSessionRevoked) {
		t.Fatalf("expected ErrAdminSessionRevoked after a session version bump, got %v", err)
	}
}

func TestAuthenticateRejectsBumpedAuthVersion(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	u := store.users[tokens.User.ID]
	u.AuthVersion++
	store.users[u.ID] = u

	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminSessionExpired) {
		t.Fatalf("expected ErrAdminSessionExpired after an auth version bump, got %v", err)
	}
}

func TestAuthenticateRejectsDisabledAccount(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	u.Status = model.AdminStatusDisabled
	store.users[u.ID] = u

	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminUserDisabled) {
		t.Fatalf("expected ErrAdminUserDisabled, got %v", err)
	}
	if store.liveSessions() != 0 {
		t.Fatal("disabling an account must revoke its sessions")
	}
}

func TestAuthenticateRejectsGarbageToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	for _, tok := range []string{"", "not-a-jwt", "a.b.c", strings.Repeat("x", 40)} {
		if _, err := svc.authenticate(context.Background(), tok); !errors.Is(err, ErrAdminUnauthenticated) {
			t.Fatalf("%q: expected ErrAdminUnauthenticated, got %v", tok, err)
		}
	}
}

func TestAuthenticateRejectsTokenSignedWithAnotherSecret(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	other, _, _ := newTestService(t)
	other.secret = []byte("a-completely-different-secret")
	if _, err := other.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminUnauthenticated) {
		t.Fatalf("expected ErrAdminUnauthenticated, got %v", err)
	}
}

// --- logout and session management ----------------------------------------

func TestLogoutRevokesOnlyTheGivenSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	if err := svc.logout(context.Background(), a.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if store.sessions[a.Session.SID].RevokeReason != revokeReasonLogout {
		t.Fatal("logout must record its own revocation reason")
	}
	if _, err := svc.authenticate(context.Background(), b.AccessToken); err != nil {
		t.Fatalf("the other session must survive: %v", err)
	}
}

func TestLogoutIsIdempotentAndTolerant(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	for i := 0; i < 3; i++ {
		if err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
			t.Fatalf("logout %d: %v", i, err)
		}
	}
	if err := svc.logout(context.Background(), "garbage"); err != nil {
		t.Fatalf("a malformed token must still return success: %v", err)
	}
}

func TestListSessionsExcludesRevoked(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	_ = svc.logout(context.Background(), a.RefreshToken)
	items, err := svc.listSessions(context.Background(), b.User.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].SID != b.Session.SID {
		t.Fatalf("expected only the live session, got %+v", items)
	}
}

func TestRevokeSessionRejectsAnotherAccountsSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "victim", "Victim#2026Pass")
	victimTokens, _ := svc.login(context.Background(), "victim", "Victim#2026Pass", "10.0.0.7", "")

	// A second account tries to revoke the first account's session.
	other := model.AdminUser{ID: uuid.NewString(), Username: "attacker", Status: model.AdminStatusEnabled, AuthVersion: 1}
	store.users[other.ID] = other

	if err := svc.revokeSession(context.Background(), other.ID, victimTokens.Session.SID); !errors.Is(err, ErrAdminSessionNotFound) {
		t.Fatalf("expected ErrAdminSessionNotFound, got %v", err)
	}
	if store.sessions[victimTokens.Session.SID].RevokedAt != nil {
		t.Fatal("the victim's session must not be revoked")
	}
}

func TestRevokeOwnSessionWorks(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	if err := svc.revokeSession(context.Background(), tokens.User.ID, tokens.Session.SID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.authenticate(context.Background(), tokens.AccessToken); !errors.Is(err, ErrAdminSessionRevoked) {
		t.Fatalf("expected ErrAdminSessionRevoked, got %v", err)
	}
}

// --- password change -------------------------------------------------------

func TestChangePasswordRevokesEverything(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	if err := svc.changePassword(context.Background(), a.User.ID, "Campus#2026Gate", "NewCampus#2027"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if store.users[a.User.ID].AuthVersion != a.User.AuthVersion+1 {
		t.Fatal("a password change must bump auth_version")
	}
	if store.liveSessions() != 0 {
		t.Fatalf("a password change must revoke every session, %d live", store.liveSessions())
	}
	// A password change revokes the sessions outright, so the surviving tokens
	// report "revoked" rather than "expired". Either way they are unusable.
	for _, tok := range []string{a.AccessToken, b.AccessToken} {
		if _, err := svc.authenticate(context.Background(), tok); !errors.Is(err, ErrAdminSessionRevoked) {
			t.Fatalf("expected ErrAdminSessionRevoked, got %v", err)
		}
	}
	if store.sessions[a.Session.SID].RevokeReason != revokeReasonPassword {
		t.Fatalf("expected reason %q, got %q", revokeReasonPassword, store.sessions[a.Session.SID].RevokeReason)
	}
	// The new password works, the old one does not.
	if _, err := svc.login(context.Background(), "campus-admin", "NewCampus#2027", "10.0.0.5", ""); err != nil {
		t.Fatalf("new password must work: %v", err)
	}
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", ""); !errors.Is(err, ErrAdminCredentialsInvalid) {
		t.Fatalf("old password must fail, got %v", err)
	}
}

func TestChangePasswordRejectsWrongCurrent(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	if err := svc.changePassword(context.Background(), u.ID, "NotThePassword", "NewCampus#2027"); !errors.Is(err, ErrAdminCredentialsInvalid) {
		t.Fatalf("expected ErrAdminCredentialsInvalid, got %v", err)
	}
	if store.users[u.ID].AuthVersion != u.AuthVersion {
		t.Fatal("a rejected change must not bump auth_version")
	}
}

func TestChangePasswordRejectsWeakAndUnchanged(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	if err := svc.changePassword(context.Background(), u.ID, "Campus#2026Gate", "short"); !errors.Is(err, ErrAdminPasswordWeak) {
		t.Fatalf("expected ErrAdminPasswordWeak, got %v", err)
	}
	if err := svc.changePassword(context.Background(), u.ID, "Campus#2026Gate", "Campus#2026Gate"); !errors.Is(err, ErrAdminPasswordUnchanged) {
		t.Fatalf("expected ErrAdminPasswordUnchanged, got %v", err)
	}
}

func TestChangePasswordWorksForNonSuperAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)
	hash, _ := security.HashPassword("Auditor#2026Pass")
	u, err := store.CreateFirstAdminUser(context.Background(), "auditor", "Auditor", hash, []string{"auditor"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.changePassword(context.Background(), u.ID, "Auditor#2026Pass", "Auditor#2027Pass"); err != nil {
		t.Fatalf("self password change must not depend on RBAC: %v", err)
	}
}

// --- password policy -------------------------------------------------------

func TestValidateAdminPasswordPolicy(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{"ten runes with two classes", "admin", "abcd123456", false},
		{"long passphrase with symbols", "admin", "campus-gateway-2026!", false},
		{"chinese password with digits", "admin", "能小智校园网关2026", false},
		{"too short", "admin", "abc123", true},
		{"only lowercase", "admin", "abcdefghijkl", true},
		{"only digits", "admin", "1234567890", true},
		{"only symbols", "admin", "!!!!!!!!!!", true},
		{"equals the username", "admin", "admin", true},
		{"empty", "admin", "", true},
	}
	for _, tc := range cases {
		err := validateAdminPassword(tc.username, tc.password)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected rejection for %q", tc.name, tc.password)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: unexpected rejection for %q: %v", tc.name, tc.password, err)
		}
	}
}

func TestValidateAdminPasswordRejectsOverlong(t *testing.T) {
	if err := validateAdminPassword("admin", strings.Repeat("aB3!", 60)); !errors.Is(err, ErrAdminPasswordWeak) {
		t.Fatalf("expected ErrAdminPasswordWeak, got %v", err)
	}
}

// --- error mapping ---------------------------------------------------------

func TestAdminErrorStatusMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{nil, 200, ""},
		{ErrAdminAuthDisabled, 503, "admin_api_disabled"},
		{ErrAdminCredentialsInvalid, 401, "invalid_credentials"},
		{ErrAdminUnauthenticated, 401, "unauthenticated"},
		{ErrAdminSessionExpired, 401, "session_expired"},
		{ErrAdminSessionRevoked, 401, "session_revoked"},
		{ErrAdminSessionNotFound, 401, "session_not_found"},
		{ErrAdminUserNotFound, 401, "account_not_found"},
		{ErrAdminUserDisabled, 403, "account_disabled"},
		{ErrAdminForbidden, 403, "admin_forbidden"},
		{ErrAdminSetupNotAllowed, 403, "setup_not_allowed"},
		{ErrAdminSetupRemoteForbidden, 403, "setup_required_but_remote_forbidden"},
		{ErrAdminCSRFRejected, 403, "csrf_rejected"},
		{ErrAdminRateLimited, 429, "too_many_attempts"},
		{ErrAdminPasswordWeak, 400, "password_policy_violation"},
		{ErrAdminPasswordUnchanged, 400, "password_unchanged"},
		{ErrAdminBadRequest, 400, "invalid_request"},
		{ErrAdminInternal, 500, "admin_internal_error"},
		{errors.New("unclassified"), 500, "admin_internal_error"},
	}
	for _, tc := range cases {
		status, code := adminErrorStatus(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatalf("%v: got (%d, %q), want (%d, %q)", tc.err, status, code, tc.status, tc.code)
		}
	}
}

func TestAdminErrorStatusNeverReturns503ForAuthFailures(t *testing.T) {
	// The v1 implementation mapped any error containing "required" to 503,
	// which turned an expired session into "service unavailable".
	for _, err := range []error{
		ErrAdminUnauthenticated, ErrAdminSessionExpired, ErrAdminSessionRevoked,
		ErrAdminSessionNotFound, ErrAdminCredentialsInvalid,
	} {
		if status, _ := adminErrorStatus(err); status == 503 {
			t.Fatalf("%v must not map to 503", err)
		}
	}
}

func TestAdminErrorMessageHidesInternalDetail(t *testing.T) {
	if got := adminErrorMessage(ErrAdminInternal); got != ErrAdminInternal.Error() {
		t.Fatalf("internal errors must be reported generically, got %q", got)
	}
	if got := adminErrorMessage(ErrAdminCredentialsInvalid); got != "username or password is incorrect" {
		t.Fatalf("unexpected credential message: %q", got)
	}
}

// --- role filtering --------------------------------------------------------

func TestFilterAdminRoles(t *testing.T) {
	got := filterAdminRoles([]string{"super_admin", "unknown-role", " auditor "})
	if !got["super_admin"] || !got["auditor"] || len(got) != 2 {
		t.Fatalf("unexpected roles: %v", got)
	}
	// An account with no recognisable role is a plain member. Falling back to
	// auditor would hand out read access to request logs and audit trails.
	if fallback := filterAdminRoles(nil); !fallback[RoleMember] || len(fallback) != 1 {
		t.Fatalf("an account with no known role must fall back to member, got %v", fallback)
	}
	if fallback := filterAdminRoles([]string{"nonsense"}); !fallback[RoleMember] || len(fallback) != 1 {
		t.Fatalf("an account with only unknown roles must be a member, got %v", fallback)
	}
	// member and a management role must never coexist: whichever check ran
	// first would otherwise decide whether the account is an administrator.
	mixed := filterAdminRoles([]string{"member", "gateway_admin"})
	if mixed[RoleMember] || !mixed[RoleGatewayAdmin] || len(mixed) != 1 {
		t.Fatalf("member must be dropped once a management role is present, got %v", mixed)
	}
	if only := filterAdminRoles([]string{"member"}); !only[RoleMember] || len(only) != 1 {
		t.Fatalf("a registered member must stay a member, got %v", only)
	}
}

func TestNormalizeAccountRolesIsDeterministicAndLeastPrivilege(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty becomes member", nil, []string{RoleMember}},
		{"unknown dropped", []string{"root", "member"}, []string{RoleMember}},
		{"member removed with management role", []string{"member", "auditor"}, []string{RoleAuditor}},
		{"duplicates collapse", []string{"operator", "operator"}, []string{RoleOperator}},
		{"canonical order", []string{"auditor", "super_admin", "operator"}, []string{RoleSuperAdmin, RoleOperator, RoleAuditor}},
		{"trimmed", []string{"  gateway_admin  "}, []string{RoleGatewayAdmin}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeAccountRoles(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// --- concurrent refresh rotation -------------------------------------------

// TestRefreshLostCompareAndSetIsBenign reproduces the exact multi-tab
// interleaving: both requests read the session while it still held R1, one wins
// the compare-and-set, and the loser must still be answered with the successor
// instead of being signed out.
func TestRefreshLostCompareAndSetIsBenign(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	s1, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	before := store.sessions[s1.Session.SID]

	winner, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("winner refresh: %v", err)
	}

	// The loser had already read the row before the winner committed.
	store.staleRead = &before

	loser, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("the loser of the compare-and-set must not be signed out: %v", err)
	}
	if loser.RefreshToken != winner.RefreshToken {
		t.Fatalf("both tabs must receive the same successor:\nwinner=%q\n loser=%q",
			winner.RefreshToken, loser.RefreshToken)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a lost compare-and-set must not revoke the session, %d live", store.liveSessions())
	}
	if reason := store.sessions[s1.Session.SID].RevokeReason; reason != "" {
		t.Fatalf("a lost compare-and-set must not record a revoke reason, got %q", reason)
	}
}

// TestRefreshSecondReadAfterRotationIsBenign covers the ordering where the
// second request only touches the database after the first rotation finished.
func TestRefreshSecondReadAfterRotationIsBenign(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	s1, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	first, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	second, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("second refresh inside the grace window: %v", err)
	}
	if first.RefreshToken != second.RefreshToken {
		t.Fatalf("both responses must carry the same successor")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("expected the session to stay live, %d live", store.liveSessions())
	}
}

// TestRefreshRaceDoesNotRevokeOtherSessions pins the blast radius: a benign race
// on one session must leave the account's other devices alone.
func TestRefreshRaceDoesNotRevokeOtherSessions(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	if _, err := svc.refresh(context.Background(), a.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("session A first refresh: %v", err)
	}
	if _, err := svc.refresh(context.Background(), a.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("session A concurrent refresh: %v", err)
	}

	if store.liveSessions() != 2 {
		t.Fatalf("session B must survive a race on session A, %d live", store.liveSessions())
	}
	if _, err := svc.refresh(context.Background(), b.RefreshToken, "10.0.0.6", ""); err != nil {
		t.Fatalf("session B must still be able to refresh: %v", err)
	}
}

// TestDeriveNextAdminRefreshTokenIsDeterministic documents the property the
// whole rotation scheme depends on: the same presented token always yields the
// same successor, so racing callers converge instead of clobbering each other.
func TestDeriveNextAdminRefreshTokenIsDeterministic(t *testing.T) {
	secret := []byte("unit-test-signing-secret")

	plain1, hash1, err := security.DeriveNextAdminRefreshToken(secret, "sid-1", "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	plain2, hash2, err := security.DeriveNextAdminRefreshToken(secret, "sid-1", "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	if plain1 != plain2 || hash1 != hash2 {
		t.Fatalf("derivation must be deterministic: %q/%q vs %q/%q", plain1, hash1, plain2, hash2)
	}

	other, _, err := security.DeriveNextAdminRefreshToken(secret, "sid-1", "secret-2")
	if err != nil {
		t.Fatal(err)
	}
	if other == plain1 {
		t.Fatal("a different presented secret must derive a different successor")
	}

	otherSID, _, err := security.DeriveNextAdminRefreshToken(secret, "sid-2", "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	if otherSID == plain1 {
		t.Fatal("a different session must derive a different successor")
	}
}

// TestRefreshUsesDerivedSuccessorNotRandomValue pins that rotation derives the
// successor rather than drawing a fresh random secret, which is what makes the
// concurrent case converge.
func TestRefreshUsesDerivedSuccessorNotRandomValue(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	s1, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	_, presentedSecret, err := security.SplitAdminRefreshToken(s1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := security.DeriveNextAdminRefreshToken(svc.secret, s1.Session.SID, presentedSecret)
	if err != nil {
		t.Fatal(err)
	}

	out, err := svc.refresh(context.Background(), s1.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if out.RefreshToken != want {
		t.Fatalf("rotation must derive the successor:\n want=%q\n got =%q", want, out.RefreshToken)
	}
}
