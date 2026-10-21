package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
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

	// findSessionErr, when set, makes every FindAdminSessionByID call fail.
	// It models the store being unreachable, which is the case where a
	// caller must not be able to turn an outage into a revocation.
	findSessionErr error

	// createSessionErr, when set, makes the next CreateAdminSession call fail
	// and is then cleared. It models the store going away between the two
	// writes a registration performs: the account is inserted, the session is
	// not.
	createSessionErr error

	// revokeSessionsErr, when set, makes every RevokeSessionsForUser call fail.
	// It models the store going away at exactly the moment a replay has to
	// retire the session family, which is the case where the service must not
	// report a revocation that did not happen.
	revokeSessionsErr error

	// revokes records every RevokeSessionsForUser call, so a test can assert
	// that one logical mutation revoked once and not once per field.
	revokes []revokeCall

	// tenants maps a Tenant id to its display name. The fake only needs the
	// name because it is not asked to enforce quota or key membership.
	tenants map[string]string
	// ownedKeys is the number of API Keys each account owns. It stands in for a
	// real gateway_api_key count so the refusal can be exercised without a
	// database.
	ownedKeys map[string]int64
}

// revokeCall is one recorded family revocation.
type revokeCall struct {
	UserID string
	Reason string
}

func newFakeAdminStore() *fakeAdminStore {
	return &fakeAdminStore{
		users:     map[string]model.AdminUser{},
		names:     map[string]string{},
		sessions:  map[string]model.AdminSession{},
		tenants:   map[string]string{},
		ownedKeys: map[string]int64{},
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

// UpdateAdminUserAccount mirrors the real transaction: compute the final state,
// judge the super-administrator invariant once against that outcome, bump
// auth_version at most once, and leave the row completely untouched when nothing
// changed.
func (f *fakeAdminStore) UpdateAdminUserAccount(_ context.Context, id string, m storage.AdminUserMutation) (model.AdminUser, storage.AdminUserChangeSet, error) {
	current, ok := f.users[id]
	if !ok {
		return model.AdminUser{}, storage.AdminUserChangeSet{}, storage.ErrAdminUserNotFound
	}

	next := current
	if m.Roles != nil {
		next.Roles = *m.Roles
	}
	if m.Status != nil {
		next.Status = *m.Status
	}
	if m.DisplayName != nil {
		next.DisplayName = *m.DisplayName
	}

	changes := storage.AdminUserChangeSet{
		OldRoles:       current.Roles,
		NewRoles:       next.Roles,
		OldStatus:      current.Status,
		NewStatus:      next.Status,
		OldDisplayName: current.DisplayName,
		NewDisplayName: next.DisplayName,
	}
	changes.RolesChanged = canonicalRoles(current.Roles) != canonicalRoles(next.Roles)
	changes.StatusChanged = current.Status != next.Status
	changes.DisplayNameChanged = current.DisplayName != next.DisplayName

	// The real store evaluates this against the final state, inside the same
	// transaction as the write; the fake must not be more permissive than that
	// or the service tests would pass against behaviour that does not exist.
	if isEnabledSuperAdmin(current) && !isEnabledSuperAdmin(next) && f.enabledSuperAdmins() <= 1 {
		return model.AdminUser{}, storage.AdminUserChangeSet{}, storage.ErrLastSuperAdmin
	}

	// A no-op is not a write.
	if !changes.AnythingChanged() {
		return current, changes, nil
	}

	if changes.AuthenticationChanged() {
		next.AuthVersion++
	}
	next.UpdatedAt = time.Now().UTC()
	f.users[id] = next
	return next, changes, nil
}

// canonicalRoles gives a role list a comparable form so a reordered list is not
// mistaken for a change.
func canonicalRoles(roles []string) string {
	return strings.Join(NormalizeAccountRoles(roles), ",")
}

func isEnabledSuperAdmin(u model.AdminUser) bool {
	return u.Status == model.AdminStatusEnabled && hasSuperAdmin(u.Roles)
}

// AssignAdminUserTenant mirrors the real transaction's decisions: the Tenant
// must exist, an account that owns keys is refused with the count, and the
// membership write leaves auth_version alone.
func (f *fakeAdminStore) AssignAdminUserTenant(_ context.Context, id, tenantID string) (model.AdminUser, error) {
	u, ok := f.users[id]
	if !ok {
		return model.AdminUser{}, storage.ErrAdminUserNotFound
	}
	if tenantID != "" {
		if _, exists := f.tenants[tenantID]; !exists {
			return model.AdminUser{}, storage.ErrAdminTenantNotFound
		}
	}
	if owned := f.ownedKeys[id]; owned > 0 {
		return model.AdminUser{}, &storage.AdminUserOwnsKeysError{Count: owned}
	}
	u.TenantID = tenantID
	u.TenantName = f.tenants[tenantID]
	u.UpdatedAt = time.Now().UTC()
	f.users[id] = u
	return u, nil
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
	if f.createSessionErr != nil {
		err := f.createSessionErr
		f.createSessionErr = nil
		return model.AdminSession{}, err
	}
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
	if f.findSessionErr != nil {
		return model.AdminSession{}, f.findSessionErr
	}
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
	if f.revokeSessionsErr != nil {
		return f.revokeSessionsErr
	}
	f.revokes = append(f.revokes, revokeCall{UserID: userID, Reason: reason})
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

// revokeCallsFor counts how many times an account's sessions were revoked.
func (f *fakeAdminStore) revokeCallsFor(userID string) int {
	n := 0
	for _, r := range f.revokes {
		if r.UserID == userID {
			n++
		}
	}
	return n
}

// lastRevokeReason is the reason recorded by the most recent revocation of an
// account's sessions.
func (f *fakeAdminStore) lastRevokeReason(userID string) string {
	reason := ""
	for _, r := range f.revokes {
		if r.UserID == userID {
			reason = r.Reason
		}
	}
	return reason
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

// fakeRegistrationLimiter is an in-memory registrationLimiter.
//
// It mirrors the real limiter's shape: one call increments and decides, and the
// count is compared after the increment, so a limit of five admits exactly five.
// broken models a Redis outage, which registration must fail closed on.
type fakeRegistrationLimiter struct {
	mu     sync.Mutex
	max    int64
	window time.Duration
	counts map[string]int64
	// attempts counts every consumed unit of budget, which is what the tests
	// assert on when they check that a failed attempt still costs.
	attempts int64
	broken   bool
}

func newFakeRegistrationLimiter(max int64) *fakeRegistrationLimiter {
	return &fakeRegistrationLimiter{max: max, window: time.Hour, counts: map[string]int64{}}
}

func (l *fakeRegistrationLimiter) ConsumeRegistration(_ context.Context, ip string) (bool, time.Duration, error) {
	if l.broken {
		return false, 0, errors.New("limiter unavailable")
	}
	if l.max <= 0 {
		return true, 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[ip]++
	l.attempts++
	if l.counts[ip] > l.max {
		return false, l.window, nil
	}
	return true, 0, nil
}

// consumed reports how many units of budget have been charged.
func (l *fakeRegistrationLimiter) consumed() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attempts
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
	if !strings.HasPrefix(tokens.RefreshToken, "v2."+tokens.Session.SID+".") {
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

// --- the five-second default replay grace -----------------------------------
//
// P1-04 (confirmed and implemented). The shipped default for
// ADMIN_REFRESH_REPLAY_GRACE is five seconds. These tests pin the behaviour at
// that value with an injected clock — none of them sleeps.
//
// What the window is for, so it is not mistaken for more: two refreshes that
// genuinely race converge on the same successor instead of one being treated
// as a stolen credential. It does not make a stolen *current* token useless,
// and it does not replace HttpOnly, Secure, SameSite, rotation, replay
// detection or session-family revocation.

// graceService builds a service whose replay grace is the shipped default, so
// the behaviour under test is the behaviour production gets.
func graceService(t *testing.T) (*adminAuthService, *fakeAdminStore) {
	t.Helper()
	cfg := testConfig()
	cfg.AdminRefreshReplayGrace = adminRefreshReplayGraceDefault
	store := newFakeAdminStore()
	svc := newAdminAuthService(store, newFakeLimiter(8), []byte("unit-test-signing-secret"), cfg).
		withRegistrationLimiter(newFakeRegistrationLimiter(cfg.PublicRegisterMaxAttempts))
	return svc, store
}

// T0: A → B. T0+4s: A again. Still the benign race: the same successor comes
// back, the session stays active, no replay is recorded.
func TestRefreshSupersededTokenWithinFiveSecondsIsBenign(t *testing.T) {
	svc, store := graceService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	first, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	svc.now = func() time.Time { return base.Add(4 * time.Second) }
	second, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("a superseded token inside the five-second window must be accepted: %v", err)
	}
	if second.RefreshToken != first.RefreshToken {
		t.Fatal("both racing refreshes must derive the identical successor")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a benign race must not revoke the session, %d live", store.liveSessions())
	}
	if reason := store.sessions[tokens.Session.SID].RevokeReason; reason != "" {
		t.Fatalf("a benign race must not record a revoke reason, got %q", reason)
	}
	// The successor stays usable afterwards.
	svc.now = func() time.Time { return base.Add(4 * time.Second) }
	if _, err := svc.refresh(context.Background(), second.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("the successor must stay usable: %v", err)
	}
}

// The boundary is "not after validUntil", and it stays that way: T0+5s exactly
// is still benign; one nanosecond later is a replay that retires the family.
func TestRefreshSupersededTokenAtFiveSecondBoundary(t *testing.T) {
	svc, store := graceService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	other, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	first, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// now == validUntil: still inside the window.
	svc.now = func() time.Time { return base.Add(adminRefreshReplayGraceDefault) }
	atBoundary, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("the exact boundary must still be the benign race: %v", err)
	}
	if atBoundary.RefreshToken != first.RefreshToken {
		t.Fatal("the boundary refresh must return the identical successor")
	}

	// One nanosecond past the boundary: replay, and the whole family goes.
	svc.now = func() time.Time { return base.Add(adminRefreshReplayGraceDefault + time.Nanosecond) }
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("expected ErrAdminRefreshReplay one nanosecond past the boundary, got %v", err)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("a replay must revoke every session of the account, %d still live", store.liveSessions())
	}
	if store.sessions[other.Session.SID].RevokeReason != revokeReasonReuse {
		t.Fatalf("the second device must be revoked with the replay reason, got %q", store.sessions[other.Session.SID].RevokeReason)
	}
}

// Past the window the superseded token is a replay: ErrAdminRefreshReplay,
// every session of the account revoked, reason recorded.
func TestRefreshSupersededTokenAfterFiveSecondsIsReplay(t *testing.T) {
	svc, store := graceService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	other, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	base := time.Now()
	svc.now = func() time.Time { return base }
	rotated, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	svc.now = func() time.Time { return base.Add(adminRefreshReplayGraceDefault + time.Second) }
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("expected ErrAdminRefreshReplay past the five-second window, got %v", err)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("replay must revoke every session of the account, %d still live", store.liveSessions())
	}
	if store.sessions[other.Session.SID].RevokeReason != revokeReasonReuse {
		t.Fatalf("expected reason %q, got %q", revokeReasonReuse, store.sessions[other.Session.SID].RevokeReason)
	}
	// The token issued by the legitimate rotation is dead too.
	if _, err := svc.refresh(context.Background(), rotated.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminSessionRevoked) {
		t.Fatalf("the whole family must be revoked, got %v", err)
	}
}

// The current token is untouched by the grace window: six grace periods after
// the rotation, B still rotates normally to C, and the chain continues to D.
// The previous hash only ever represents the most recent generation.
func TestRefreshCurrentTokenUnaffectedByFiveSecondGrace(t *testing.T) {
	svc, store := graceService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if tokens.RefreshToken == "" {
		t.Fatal("login must issue a refresh token")
	}

	first, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "") // A → B
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	svc.now = func() time.Time { return base.Add(30 * time.Second) }
	second, err := svc.refresh(context.Background(), first.RefreshToken, "10.0.0.5", "") // B → C
	if err != nil {
		t.Fatalf("the current token must rotate normally long after the grace window: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("rotation must issue a new token")
	}

	third, err := svc.refresh(context.Background(), second.RefreshToken, "10.0.0.5", "") // C → D
	if err != nil {
		t.Fatalf("the chain must continue: %v", err)
	}
	if third.RefreshToken == second.RefreshToken {
		t.Fatal("rotation must issue a new token")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("normal rotations must not touch the session count, got %d", store.liveSessions())
	}
	if reason := store.sessions[tokens.Session.SID].RevokeReason; reason != "" {
		t.Fatalf("normal rotations must not record a revoke reason, got %q", reason)
	}

	// The generation before the previous one is no longer the grace subject:
	// after C → D, presenting B is a replay, not a benign race.
	if _, err := svc.refresh(context.Background(), first.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("only the most recent predecessor is inside the window, got %v", err)
	}
}

// Two refreshes genuinely in flight — the loser read the session row before
// the winner's rotation landed — still converge at five seconds, and nothing
// is revoked. The frontend's single-flight promise cannot help across runtimes
// or tabs; this is the backend mechanism that can.
func TestConcurrentRefreshWithinFiveSecondsConverges(t *testing.T) {
	svc, store := graceService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	// The row as the loser's read saw it: before the winner's rotation.
	preRotation := store.sessions[tokens.Session.SID]

	first, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// The loser's lookup returns the pre-rotation row once, exactly as a
	// concurrent request would have read it.
	store.staleRead = &preRotation

	second, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("a concurrent refresh must be accepted at five seconds too: %v", err)
	}
	if second.RefreshToken != first.RefreshToken {
		t.Fatal("both tabs must converge on the identical successor")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a benign race must not revoke anything, %d live", store.liveSessions())
	}
	if reason := store.sessions[tokens.Session.SID].RevokeReason; reason != "" {
		t.Fatalf("a benign race must not record a revoke reason, got %q", reason)
	}
	if _, err := svc.refresh(context.Background(), second.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("the successor must stay usable: %v", err)
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

	if _, err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
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

// P2-2 (review). The audit action is what the operator reads back, so a
// request that revoked nothing must not be named like one that did. The
// mapping lives in one pure function so this holds without a database.
func TestLogoutAuditAction(t *testing.T) {
	if got := logoutAuditAction(true); got != "admin_logout" {
		t.Fatalf("a real logout must be audited as admin_logout, got %q", got)
	}
	if got := logoutAuditAction(false); got != "admin_logout_rejected" {
		t.Fatalf("a no-op logout must be audited as admin_logout_rejected, got %q", got)
	}
}

func TestLogoutRevokesOnlyTheGivenSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	revoked, err := svc.logout(context.Background(), a.RefreshToken)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !revoked {
		t.Fatal("a genuine logout must report the revocation")
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
		if _, err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
			t.Fatalf("logout %d: %v", i, err)
		}
	}
	if _, err := svc.logout(context.Background(), "garbage"); err != nil {
		t.Fatalf("a malformed token must still return success: %v", err)
	}
}

// P1-04. Logout must verify the token it is handed, not only the session id
// inside it.
//
// The old implementation split the refresh token, kept the sid and revoked the
// session on that alone. Anyone who learned a sid — from an access-token claim,
// a device list, a log line, a URL — could send "<sid>.anything" and sign that
// operator's device out: a denial of service against another account, with no
// credential needed. A logout request is authorised by the refresh token
// itself, exactly like a refresh request.
//
// The forged token still gets the client-visible success and cookie clearing,
// so an attacker probing with forged sids cannot distinguish a hit from a miss.
func TestLogoutWithForgedTokenDoesNotRevoke(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	victim, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	// A second device that must be untouched by anything happening here.
	other, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	for _, forged := range []string{
		victim.Session.SID + ".anything",
		victim.Session.SID + ".aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		other.Session.SID + ".forged-secret",
	} {
		revoked, err := svc.logout(context.Background(), forged)
		if err != nil {
			t.Fatalf("a forged token must still exit cleanly, got %v", err)
		}
		// P2-2 (review): the bool is what keeps the audit trail honest — a
		// forged token must never be recorded as a logout that happened.
		if revoked {
			t.Fatalf("a forged token must not report a revocation: %s", forged)
		}
	}
	if store.liveSessions() != 2 {
		t.Fatalf("a forged token must not revoke any session, %d still live", store.liveSessions())
	}
	if store.sessions[victim.Session.SID].RevokedAt != nil {
		t.Fatal("the victim's session must survive a forged logout")
	}

	// The genuine token still works — the check narrows the forged path, it
	// does not break the honest one.
	revoked, err := svc.logout(context.Background(), victim.RefreshToken)
	if err != nil {
		t.Fatalf("the genuine token must still log the session out: %v", err)
	}
	if !revoked {
		t.Fatal("the genuine token must report the revocation")
	}
	if store.sessions[victim.Session.SID].RevokeReason != revokeReasonLogout {
		t.Fatal("the genuine logout must still record its reason")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("the other session must survive, %d live", store.liveSessions())
	}
}

// An unknown sid and an already-revoked session are not authorisation either:
// both are answered with success and change nothing.
func TestLogoutWithUnknownOrDeadTokenIsHarmless(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	live, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	// A session that was already revoked by other means: its sid is known, but
	// logging out with its (correct) token must not resurrect or re-revoke it.
	dead, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")
	if revoked, err := svc.logout(context.Background(), dead.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	} else if !revoked {
		t.Fatal("a token that owns its session must report the revocation, already applied or not")
	}
	if _, err := svc.logout(context.Background(), dead.RefreshToken); err != nil {
		t.Fatalf("a repeated logout must stay idempotent: %v", err)
	}

	for _, raw := range []string{"00000000-0000-0000-0000-000000000000.never-issued", "garbage", ""} {
		revoked, err := svc.logout(context.Background(), raw)
		if err != nil {
			t.Fatalf("logout(%q) must return success, got %v", raw, err)
		}
		// None of these identify a session this token owns, so none of them is
		// a logout — the audit must not say otherwise.
		if revoked {
			t.Fatalf("logout(%q) must not report a revocation", raw)
		}
	}
	if store.liveSessions() != 1 || store.sessions[live.Session.SID].RevokedAt != nil {
		t.Fatal("the live session must not be touched by dead-token logouts")
	}
}

// A just-rotated token is still the caller's own: two browser tabs can race a
// rotation and the loser must still be able to log its session out, inside the
// same grace window refresh() honours.
func TestLogoutWithSupersededTokenInsideGraceStillWorks(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	rotated, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// The superseded token arrives after the rotation, still inside the window.
	if _, err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
		t.Fatalf("the superseded token must be able to log out inside the grace window: %v", err)
	}
	if store.sessions[tokens.Session.SID].RevokedAt == nil {
		t.Fatal("the session must be revoked")
	}
	// The current generation can still log out too — idempotent no-op by then.
	if _, err := svc.logout(context.Background(), rotated.RefreshToken); err != nil {
		t.Fatalf("the current token must also be accepted: %v", err)
	}
}

// Outside the grace window the superseded token is no proof of ownership, so
// it must not revoke either.
func TestLogoutWithSupersededTokenOutsideGraceDoesNotRevoke(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	base := time.Now()
	svc.now = func() time.Time { return base }
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Past the grace window, the superseded token is no longer a credential
	// for this session — the same rule replay detection applies.
	svc.now = func() time.Time { return base.Add(testConfig().AdminRefreshReplayGrace + time.Second) }

	if _, err := svc.logout(context.Background(), tokens.RefreshToken); err != nil {
		t.Fatalf("logout must still return success, got %v", err)
	}
	if store.sessions[tokens.Session.SID].RevokedAt != nil {
		t.Fatal("a superseded token outside the grace window must not revoke the session")
	}
}

// A store failure is reported to the server, but the sid is never used as
// authorisation on the way out: the client exits, nothing is revoked.
func TestLogoutWithStoreFailureDoesNotRevoke(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")

	store.findSessionErr = errors.New("store unavailable")
	_, err := svc.logout(context.Background(), tokens.RefreshToken)
	if !errors.Is(err, ErrAdminInternal) {
		t.Fatalf("expected ErrAdminInternal, got %v", err)
	}
	if store.sessions[tokens.Session.SID].RevokedAt != nil {
		t.Fatal("an outage must never revoke a session")
	}
}

// P1-05 (review). One comparison definition serves both refresh-token
// generations, and it is the constant-time one. The verdicts are asserted
// here; the timing property itself is not testable — see the helper's comment.
func TestAdminRefreshDigestEqual(t *testing.T) {
	if !adminRefreshDigestEqual("abc", "abc") {
		t.Fatal("identical digests must compare equal")
	}
	if adminRefreshDigestEqual("abc", "abd") {
		t.Fatal("digests differing in the last byte must compare unequal")
	}
	// A prefix is not a match, and the length difference must not panic or
	// compare equal the way a byte-wise loop over the shorter string might.
	if adminRefreshDigestEqual("abc", "ab") {
		t.Fatal("a prefix must not compare equal")
	}
	if adminRefreshDigestEqual("ab", "abc") {
		t.Fatal("a prefix must not compare equal in either direction")
	}
	if !adminRefreshDigestEqual("", "") {
		t.Fatal("two empty digests are equal")
	}
	if adminRefreshDigestEqual("", "x") {
		t.Fatal("an empty digest must not equal a non-empty one")
	}
}

func TestListSessionsExcludesRevoked(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	a, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	b, _ := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")

	_, _ = svc.logout(context.Background(), a.RefreshToken)
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
	parsed, err := security.ParseAdminRefreshToken(svc.secret, s1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	presentedSecret := parsed.Secret
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
