package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// Password policy. The floor is a rune count, not a byte count, so a password
// written in Chinese is measured the way a user counts it.
const (
	MinAdminPasswordLength = 10
	MaxAdminPasswordLength = 200

	// adminRefreshCookieName is the HttpOnly cookie holding the refresh token.
	// Its Path is scoped to the auth endpoints so it is not attached to every
	// admin API call.
	adminRefreshCookieName = "campus_admin_refresh"
	adminRefreshCookiePath = "/admin/auth"

	// adminSessionTouchInterval throttles last_active_at writes. Admin traffic
	// is light, but a write per request is still wasteful.
	adminSessionTouchInterval = 60 * time.Second

	// Revocation reasons recorded in gateway_admin_session.revoke_reason.
	//
	// They are distinct so the device list and the audit trail say what
	// happened. One PATCH produces one reason even when it changed several
	// fields, because it was one logical change.
	revokeReasonLogout          = "logout"
	revokeReasonPassword        = "password_changed"
	revokeReasonReuse           = "refresh_reuse_detected"
	revokeReasonDisabled        = "account_disabled"
	revokeReasonStale           = "auth_version_mismatch"
	revokeReasonAdminRevoked    = "revoked_by_admin"
	revokeReasonRoleChanged     = "role_changed"
	revokeReasonStatusChanged   = "account_status_changed"
	revokeReasonSecurityChanged = "account_security_changed"
	// revokeReasonSignInFailed retires a session whose sign-in could not be
	// completed — the row was committed, but the token never reached the
	// client. It is distinct from every other reason so a device list can say
	// "this was never a usable session" rather than "this was logged out".
	revokeReasonSignInFailed = "signin_failed"

	// Session login methods recorded in gateway_admin_session.login_method.
	loginMethodPassword = "password"
	loginMethodRegister = "register"
)

// adminStore is the slice of the storage layer the auth service depends on.
// Narrowing it here keeps the service unit-testable with an in-memory fake.
type adminStore interface {
	CountSuperAdmins(ctx context.Context) (int64, error)
	AdminUserStats(ctx context.Context) (model.AdminUserStats, error)
	FindAdminUserByUsername(ctx context.Context, username string) (model.AdminUser, error)
	FindAdminUserByID(ctx context.Context, id string) (model.AdminUser, error)
	ListAdminUsers(ctx context.Context, limit int) ([]model.AdminUser, error)
	CreateFirstAdminUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error)
	CreateUser(ctx context.Context, username, displayName, passwordHash string, roles []string) (model.AdminUser, error)
	// UpdateAdminUserAccount is the only way an existing account is changed.
	// Roles, status and display name go through one transaction so a rejected
	// patch cannot half-apply, and so auth_version moves at most once.
	UpdateAdminUserAccount(ctx context.Context, id string, mutation storage.AdminUserMutation) (model.AdminUser, storage.AdminUserChangeSet, error)
	UpdateAdminUserPassword(ctx context.Context, id, passwordHash string) error
	RehashAdminUserPassword(ctx context.Context, id, passwordHash string) error
	TouchAdminUserLogin(ctx context.Context, id string)

	CreateAdminSession(ctx context.Context, s model.AdminSession) (model.AdminSession, error)
	FindAdminSessionByID(ctx context.Context, sid string) (model.AdminSession, error)
	RotateAdminSessionRefresh(ctx context.Context, sid, oldHash, newHash string, graceUntil time.Time) (bool, error)
	TouchAdminSession(ctx context.Context, sid string)
	RevokeAdminSession(ctx context.Context, sid, reason string) error
	RevokeSessionsForUser(ctx context.Context, userID, reason string) error
	ListAdminSessions(ctx context.Context, userID string) ([]model.AdminSession, error)
}

// adminLoginLimiter throttles failed logins. Implementations may be Redis-backed
// in production and in-memory in tests.
type adminLoginLimiter interface {
	// AllowAdminLogin reports whether another attempt is permitted and, when it
	// is not, how long the caller should wait.
	AllowAdminLogin(ctx context.Context, ip, username string) (bool, time.Duration, error)
	// RecordAdminLoginFailure advances the counters after a rejected attempt.
	RecordAdminLoginFailure(ctx context.Context, ip, username string) error
	// ResetAdminLogin clears the counters after a successful login.
	ResetAdminLogin(ctx context.Context, ip, username string) error
}

// adminAuthService owns the whole credential lifecycle: password hashing,
// session creation, access-token issuance and refresh rotation. It also owns
// public registration, because a registration is just an account creation
// followed by a sign-in.
type adminAuthService struct {
	store   adminStore
	limiter adminLoginLimiter
	secret  []byte

	// registration throttles public account creation. It is nil when the
	// deployment has no registration path, and the register method tolerates
	// that so tests can exercise the flow without a cache.
	registration registrationLimiter

	accessTTL   time.Duration
	refreshTTL  time.Duration
	maxAttempts int64
	window      time.Duration

	// grace is how long a just-superseded refresh token is still accepted as a
	// benign concurrent refresh. See refresh() for the full rationale.
	grace time.Duration

	// mint signs access tokens. It is a field rather than a direct call so a
	// test can make minting fail after the session row is committed — the one
	// interleaving that would otherwise leave a session nobody holds a token
	// for.
	mint func(user model.AdminUser, session model.AdminSession) (string, time.Time, error)

	// now is injectable so tests can control expiry without sleeping.
	now func() time.Time
}

func newAdminAuthService(store adminStore, limiter adminLoginLimiter, secret []byte, cfg Config) *adminAuthService {
	a := &adminAuthService{
		store:       store,
		limiter:     limiter,
		secret:      secret,
		accessTTL:   cfg.AdminAccessTTL,
		refreshTTL:  cfg.AdminRefreshTTL,
		maxAttempts: cfg.AdminLoginMaxAttempts,
		window:      cfg.AdminLoginWindow,
		grace:       cfg.AdminRefreshReplayGrace,
		now:         time.Now,
	}
	a.mint = a.mintAccessToken
	return a
}

// withRegistrationLimiter attaches the public-registration throttle. It is a
// setter rather than a constructor parameter so the session layer's own tests
// can keep building a service without one.
func (a *adminAuthService) withRegistrationLimiter(l registrationLimiter) *adminAuthService {
	a.registration = l
	return a
}

// adminTokens is what a successful login or refresh hands back to the HTTP
// layer. RefreshToken is plaintext and only ever goes into the cookie.
type adminTokens struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	Session          model.AdminSession
	User             model.AdminUser
}

// --- first-run setup -------------------------------------------------------

// needsSetup reports whether the system still has to be initialised.
//
// The question is "is there a super administrator", not "is the account table
// empty". Public registration can create members at any time, and those must
// not close the setup window: otherwise a remote visitor could register a
// throwaway member and leave the deployment with no administrator at all.
func (a *adminAuthService) needsSetup(ctx context.Context) (bool, error) {
	n, err := a.store.CountSuperAdmins(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// setup creates the first administrator. The storage layer guards the insert
// with NOT EXISTS, so a concurrent second call cannot create a second account.
func (a *adminAuthService) setup(ctx context.Context, username, displayName, password string) (model.AdminUser, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return model.AdminUser{}, ErrAdminBadRequest
	}
	if utf8.RuneCountInString(username) > 64 {
		return model.AdminUser{}, ErrAdminBadRequest
	}
	if err := validateAdminPassword(username, password); err != nil {
		return model.AdminUser{}, err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return model.AdminUser{}, ErrAdminInternal
	}
	user, err := a.store.CreateFirstAdminUser(ctx, username, strings.TrimSpace(displayName), hash, []string{"super_admin"})
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			// The NOT EXISTS guard matched nothing: an account already exists.
			return model.AdminUser{}, ErrAdminSetupNotAllowed
		}
		return model.AdminUser{}, err
	}
	return user, nil
}

// --- login -----------------------------------------------------------------

// login verifies credentials and opens a session. Every failure that could
// reveal whether a username exists returns ErrAdminCredentialsInvalid.
func (a *adminAuthService) login(ctx context.Context, username, password, ip, userAgent string) (adminTokens, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return adminTokens{}, ErrAdminCredentialsInvalid
	}

	// Throttling fails open when Redis is unavailable: locking every
	// administrator out of the console during a cache outage is worse than
	// briefly losing the counter. The failure is logged rather than swallowed.
	if allowed, _, err := a.limiter.AllowAdminLogin(ctx, ip, username); err != nil {
		log.Printf("admin login: rate limiter unavailable, allowing the attempt: %v", err)
	} else if !allowed {
		return adminTokens{}, ErrAdminRateLimited
	}

	user, err := a.store.FindAdminUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			// Burn comparable CPU so a missing username is not measurably
			// faster than a wrong password, then count the failure.
			security.DummyVerify(password)
			a.recordLoginFailure(ctx, ip, username)
			return adminTokens{}, ErrAdminCredentialsInvalid
		}
		return adminTokens{}, ErrAdminInternal
	}

	valid, needsRehash, err := security.VerifyPassword(user.PasswordHash, password)
	if err != nil {
		// A malformed stored hash is an operational problem, not a bad login.
		log.Printf("admin login: stored password hash for %s cannot be parsed: %v", user.Username, err)
		return adminTokens{}, ErrAdminInternal
	}
	if !valid {
		a.recordLoginFailure(ctx, ip, username)
		return adminTokens{}, ErrAdminCredentialsInvalid
	}
	if user.Status != model.AdminStatusEnabled {
		a.recordLoginFailure(ctx, ip, username)
		return adminTokens{}, ErrAdminUserDisabled
	}

	// Transparent upgrade: rewrite a bcrypt (or weak-parameter) hash without
	// bumping auth_version, so the user's other devices stay signed in.
	if needsRehash {
		if newHash, err := security.HashPassword(password); err == nil {
			if err := a.store.RehashAdminUserPassword(ctx, user.ID, newHash); err == nil {
				user.PasswordHash = newHash
			}
		}
	}

	tokens, err := a.openSession(ctx, user, ip, userAgent, loginMethodPassword)
	if err != nil {
		return adminTokens{}, err
	}
	if err := a.limiter.ResetAdminLogin(ctx, ip, username); err != nil {
		log.Printf("admin login: could not reset the throttle counters: %v", err)
	}
	a.store.TouchAdminUserLogin(ctx, user.ID)
	return tokens, nil
}

// recordLoginFailure advances the throttle counters and reports a limiter
// outage instead of hiding it.
func (a *adminAuthService) recordLoginFailure(ctx context.Context, ip, username string) {
	if err := a.limiter.RecordAdminLoginFailure(ctx, ip, username); err != nil {
		log.Printf("admin login: could not record the failed attempt: %v", err)
	}
}

// openSession creates the session row and mints both tokens.
//
// loginMethod is recorded on the session so the device list and the audit trail
// can tell a sign-in from an account that was created and signed in at once.
func (a *adminAuthService) openSession(ctx context.Context, user model.AdminUser, ip, userAgent, loginMethod string) (adminTokens, error) {
	now := a.now()
	expiresAt := now.Add(a.refreshTTL)

	// The session id is generated here rather than by the database so the
	// refresh token can be derived from it before the single INSERT.
	sid := uuid.NewString()
	refreshPlain, refreshHash, err := security.NewAdminRefreshToken(sid)
	if err != nil {
		return adminTokens{}, ErrAdminInternal
	}

	created, err := a.store.CreateAdminSession(ctx, model.AdminSession{
		SID:             sid,
		UserID:          user.ID,
		UserAuthVersion: user.AuthVersion,
		RefreshHash:     refreshHash,
		LoginMethod:     loginMethod,
		IP:              truncate(ip, 64),
		UserAgent:       truncate(userAgent, 256),
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return adminTokens{}, ErrAdminInternal
	}

	access, accessExp, err := a.mint(user, created)
	if err != nil {
		// The session row is already committed, but the token that would have
		// gone with it never existed. Leaving the row live would strand a
		// session nobody can use: the account's device list would show a
		// device that never existed, and the row would sit there until its
		// refresh TTL expires. Retiring it here is what keeps the session list
		// explainable — the account survives, and the user's next step is to
		// log in, which the caller reports as a partial success.
		//
		// The revocation is best-effort: if the store is the thing that is
		// broken, the error the caller sees is still the internal one.
		_ = a.store.RevokeAdminSession(ctx, created.SID, revokeReasonSignInFailed)
		return adminTokens{}, ErrAdminInternal
	}
	return adminTokens{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshExpiresAt: expiresAt,
		Session:          created,
		User:             user,
	}, nil
}

func (a *adminAuthService) mintAccessToken(user model.AdminUser, session model.AdminSession) (string, time.Time, error) {
	now := a.now()
	expiresAt := now.Add(a.accessTTL)
	token, err := security.SignAdminAccessToken(a.secret, security.AdminAccessClaims{
		Subject:         user.ID,
		SessionID:       session.SID,
		UserAuthVersion: user.AuthVersion,
		SessionVersion:  session.Version,
		IssuedAt:        now,
		NotBefore:       now,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// --- refresh rotation ------------------------------------------------------

// refresh validates a refresh token, rotates it, and issues a new access token.
//
// Rotation is replay-safe under concurrency, which matters because two browser
// tabs are separate JavaScript runtimes and cannot coordinate with each other:
//
//   - The successor of a token is derived deterministically from the session id
//     and the presented secret, so both tabs compute byte-identical successors.
//     Whichever request wins the database compare-and-set, both responses carry
//     the same valid cookie instead of one of them clearing the other's.
//   - The superseded hash is parked in previous_refresh_hash for a short grace
//     window, so a token that arrives just after the winner's rotation is
//     recognised as a benign race and answered with 200 plus the same successor.
//   - Only a token that is neither the current hash nor a grace-window
//     predecessor is a replay. Only then is the account's session family
//     revoked.
func (a *adminAuthService) refresh(ctx context.Context, refreshToken, ip, userAgent string) (adminTokens, error) {
	sid, secret, err := security.SplitAdminRefreshToken(strings.TrimSpace(refreshToken))
	if err != nil {
		return adminTokens{}, ErrAdminUnauthenticated
	}

	session, err := a.store.FindAdminSessionByID(ctx, sid)
	if err != nil {
		if errors.Is(err, storage.ErrAdminSessionNotFound) {
			return adminTokens{}, ErrAdminSessionNotFound
		}
		return adminTokens{}, ErrAdminInternal
	}

	now := a.now()
	if session.Status != model.AdminSessionActive || session.RevokedAt != nil {
		return adminTokens{}, ErrAdminSessionRevoked
	}
	if !now.Before(session.ExpiresAt) {
		return adminTokens{}, ErrAdminSessionExpired
	}

	user, err := a.store.FindAdminUserByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			return adminTokens{}, ErrAdminUserNotFound
		}
		return adminTokens{}, ErrAdminInternal
	}
	if user.Status != model.AdminStatusEnabled {
		_ = a.store.RevokeSessionsForUser(ctx, user.ID, revokeReasonDisabled)
		return adminTokens{}, ErrAdminUserDisabled
	}
	if session.UserAuthVersion != user.AuthVersion {
		// The password changed (or the account was reset) after this session was
		// opened; the session is stale.
		_ = a.store.RevokeAdminSession(ctx, session.SID, revokeReasonStale)
		return adminTokens{}, ErrAdminSessionExpired
	}

	// Deterministic successor: identical for every caller presenting this token.
	nextPlain, nextHash, err := security.DeriveNextAdminRefreshToken(a.secret, session.SID, secret)
	if err != nil {
		return adminTokens{}, ErrAdminUnauthenticated
	}

	presented := security.HashAdminRefreshToken(strings.TrimSpace(refreshToken))

	switch {
	case adminRefreshDigestEqual(presented, session.RefreshHash):
		rotated, err := a.store.RotateAdminSessionRefresh(ctx, session.SID, presented, nextHash, now.Add(a.grace))
		if err != nil {
			return adminTokens{}, ErrAdminInternal
		}
		if rotated {
			return a.finishRefresh(session, user, nextPlain, now)
		}
		// Lost the compare-and-set: a concurrent request rotated first. Re-read
		// the row and decide between the benign race and a replay. Losing the
		// CAS alone is never evidence of compromise.
		fresh, err := a.store.FindAdminSessionByID(ctx, session.SID)
		if err != nil {
			if errors.Is(err, storage.ErrAdminSessionNotFound) {
				return adminTokens{}, ErrAdminSessionNotFound
			}
			return adminTokens{}, ErrAdminInternal
		}
		if a.isBenignRace(fresh, presented, now) {
			return a.finishRefresh(session, user, nextPlain, now)
		}
		return a.replayDetected(ctx, session.UserID)

	case a.isBenignRace(session, presented, now):
		// This request read the row before the winner's rotation landed. The
		// successor is the same token the winner produced, so hand it back
		// rather than signing the operator out.
		return a.finishRefresh(session, user, nextPlain, now)

	default:
		return a.replayDetected(ctx, session.UserID)
	}
}

// adminRefreshDigestEqual compares two refresh-token digests in constant time.
//
// The digest is not a secret in the cryptographic sense — it is a SHA-256 over
// 256 bits of CSPRNG output — but it is exactly the value an attacker has to
// match to rotate or revoke a session, so the comparison must not leak how
// many leading bytes were right. Go's == short-circuits at the first differing
// byte; this does not. One definition serves both the current and the
// superseded generation, so there is exactly one comparison to audit.
//
// The constant-time property is a property of the implementation, not of a
// test: a unit test can assert the verdicts below, but it cannot observe
// timing. The guarantee comes from crypto/subtle.ConstantTimeCompare itself
// and from there being no other Go-side digest comparison in the service — the
// other comparison (rotation's compare-and-set) happens inside PostgreSQL's
// WHERE clause, which is not a Go timing surface.
func adminRefreshDigestEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// isBenignRace reports whether a presented hash is the session's just-superseded
// refresh hash and still inside the grace window.
func (a *adminAuthService) isBenignRace(session model.AdminSession, presented string, now time.Time) bool {
	if session.PreviousRefreshHash == "" || !adminRefreshDigestEqual(presented, session.PreviousRefreshHash) {
		return false
	}
	if session.PreviousRefreshValidUntil == nil {
		return false
	}
	return !now.After(*session.PreviousRefreshValidUntil)
}

// finishRefresh mints the access token that accompanies a successful rotation.
//
// A failure here is not the same as a failure in openSession: the rotation has
// already committed, and the client still holds the superseded token, so a
// retry lands inside the grace window and succeeds. Revoking on a mint failure
// would turn a recoverable race into a forced re-login, so the session is left
// alone and the caller sees the internal error.
func (a *adminAuthService) finishRefresh(session model.AdminSession, user model.AdminUser, nextPlain string, now time.Time) (adminTokens, error) {
	access, accessExp, err := a.mint(user, session)
	if err != nil {
		return adminTokens{}, ErrAdminInternal
	}
	session.LastActiveAt = now
	return adminTokens{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     nextPlain,
		RefreshExpiresAt: session.ExpiresAt,
		Session:          session,
		User:             user,
	}, nil
}

// replayDetected retires the whole session family of an account. A refresh
// token that is neither the current hash nor a grace-window predecessor means a
// copy leaked, so every session is revoked rather than just this one.
func (a *adminAuthService) replayDetected(ctx context.Context, userID string) (adminTokens, error) {
	_ = a.store.RevokeSessionsForUser(ctx, userID, revokeReasonReuse)
	log.Printf("admin refresh: refresh token reuse outside the grace window for user %s; all sessions revoked", userID)
	return adminTokens{}, ErrAdminRefreshReplay
}

// --- access token verification --------------------------------------------

// authenticate validates an access token and re-derives the principal from the
// database, so role and status changes apply immediately instead of at the next
// token refresh.
func (a *adminAuthService) authenticate(ctx context.Context, accessToken string) (adminPrincipal, error) {
	claims, err := security.VerifyAdminAccessToken(a.secret, accessToken)
	if err != nil {
		return adminPrincipal{}, tokenErrorToSentinel(err)
	}

	session, err := a.store.FindAdminSessionByID(ctx, claims.SessionID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminSessionNotFound) {
			return adminPrincipal{}, ErrAdminSessionNotFound
		}
		return adminPrincipal{}, ErrAdminInternal
	}
	if session.Status != model.AdminSessionActive || session.RevokedAt != nil {
		return adminPrincipal{}, ErrAdminSessionRevoked
	}
	if !a.now().Before(session.ExpiresAt) {
		return adminPrincipal{}, ErrAdminSessionExpired
	}
	if session.Version != claims.SessionVersion {
		return adminPrincipal{}, ErrAdminSessionRevoked
	}
	if session.UserAuthVersion != claims.UserAuthVersion {
		return adminPrincipal{}, ErrAdminSessionExpired
	}

	user, err := a.store.FindAdminUserByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			return adminPrincipal{}, ErrAdminUserNotFound
		}
		return adminPrincipal{}, ErrAdminInternal
	}
	if user.Status != model.AdminStatusEnabled {
		_ = a.store.RevokeSessionsForUser(ctx, user.ID, revokeReasonDisabled)
		return adminPrincipal{}, ErrAdminUserDisabled
	}
	if user.AuthVersion != claims.UserAuthVersion {
		_ = a.store.RevokeAdminSession(ctx, session.SID, revokeReasonStale)
		return adminPrincipal{}, ErrAdminSessionExpired
	}

	if a.now().Sub(session.LastActiveAt) > adminSessionTouchInterval {
		a.store.TouchAdminSession(ctx, session.SID)
	}

	return adminPrincipal{
		UserID:    user.ID,
		Username:  user.Username,
		User:      user.Username,
		Roles:     filterAdminRoles(user.Roles),
		SessionID: session.SID,
		AuthMode:  "session",
	}, nil
}

// tokenErrorToSentinel translates JWT failures into admin sentinels. An expired
// or malformed token is an authentication failure, not an outage.
func tokenErrorToSentinel(err error) error {
	switch {
	case errors.Is(err, security.ErrTokenExpired),
		errors.Is(err, security.ErrTokenNotYetValid):
		return ErrAdminSessionExpired
	case errors.Is(err, security.ErrTokenMalformed),
		errors.Is(err, security.ErrTokenSignatureInvalid),
		errors.Is(err, security.ErrTokenWrongUse),
		errors.Is(err, security.ErrTokenWrongIssuer),
		errors.Is(err, security.ErrTokenWrongAudience),
		errors.Is(err, security.ErrTokenWrongAlgorithm):
		return ErrAdminUnauthenticated
	default:
		return ErrAdminUnauthenticated
	}
}

// --- logout and session management ----------------------------------------

// logout revokes the session behind a refresh token and reports whether it
// actually revoked one. It is idempotent: an unknown, malformed, already-revoked
// or expired token still returns success so a client can always clear its
// state.
//
// The session id is not authority. The token's own digest must match the
// session's current refresh hash — or its grace-window predecessor, because two
// browser tabs can race a rotation and the loser must still be able to sign its
// device out. Without that check, anyone who learned a sid from a claim, a
// device list or a log line could build "<sid>.anything" and log that operator
// out, which is a denial of service against another account.
//
// A token that fails the check gets the same client-visible success and the
// same cookie clearing as a valid one; it simply revokes nothing, so an
// attacker probing with forged sids cannot tell a hit from a miss. The bool is
// what keeps the audit trail honest: the browser always exits, but a request
// whose token did not own the session must never be recorded as a logout that
// happened.
func (a *adminAuthService) logout(ctx context.Context, refreshToken string) (bool, error) {
	raw := strings.TrimSpace(refreshToken)
	sid, _, err := security.SplitAdminRefreshToken(raw)
	if err != nil {
		// The token identifies no session at all, so there is nothing to
		// revoke and nothing to refuse.
		return false, nil
	}

	session, err := a.store.FindAdminSessionByID(ctx, sid)
	if err != nil {
		if errors.Is(err, storage.ErrAdminSessionNotFound) {
			// Never issued, or long gone: still a clean exit for the client.
			return false, nil
		}
		// The store is unreachable. The client still gets to exit — the
		// handler clears the cookie either way — but an unverified sid is
		// never turned into a revocation on the way out.
		log.Printf("admin logout: session lookup failed: %v", err)
		return false, ErrAdminInternal
	}

	if !a.logoutTokenOwnsSession(session, raw) {
		log.Printf("admin logout: the presented refresh token does not match the session it claims; nothing revoked")
		return false, nil
	}

	if err := a.store.RevokeAdminSession(ctx, session.SID, revokeReasonLogout); err != nil {
		return false, ErrAdminInternal
	}
	return true, nil
}

// logoutTokenOwnsSession reports whether the presented token is the session's
// own refresh token: either the current generation, or the superseded one still
// inside the concurrent-refresh grace window. The comparison is constant-time
// and runs on digests, so neither the stored material nor the comparison
// itself leaks the token.
func (a *adminAuthService) logoutTokenOwnsSession(session model.AdminSession, raw string) bool {
	presented := security.HashAdminRefreshToken(raw)
	if adminRefreshDigestEqual(presented, session.RefreshHash) {
		return true
	}
	return a.isBenignRace(session, presented, a.now())
}

// listSessions returns the caller's live sessions.
func (a *adminAuthService) listSessions(ctx context.Context, userID string) ([]model.AdminSession, error) {
	items, err := a.store.ListAdminSessions(ctx, userID)
	if err != nil {
		return nil, ErrAdminInternal
	}
	return items, nil
}

// revokeSession revokes one of the caller's own sessions. A session belonging
// to another account is reported as not found rather than forbidden, so the
// endpoint cannot be used to probe for session ids.
func (a *adminAuthService) revokeSession(ctx context.Context, userID, sid string) error {
	session, err := a.store.FindAdminSessionByID(ctx, sid)
	if err != nil {
		if errors.Is(err, storage.ErrAdminSessionNotFound) {
			return ErrAdminSessionNotFound
		}
		return ErrAdminInternal
	}
	if session.UserID != userID {
		return ErrAdminSessionNotFound
	}
	if err := a.store.RevokeAdminSession(ctx, sid, revokeReasonAdminRevoked); err != nil {
		return ErrAdminInternal
	}
	return nil
}

// --- password change -------------------------------------------------------

// changePassword verifies the current password, stores the new one and revokes
// every session of the account. It is deliberately independent of RBAC: any
// signed-in administrator may rotate their own password.
func (a *adminAuthService) changePassword(ctx context.Context, userID, current, next string) error {
	user, err := a.store.FindAdminUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			return ErrAdminUserNotFound
		}
		return ErrAdminInternal
	}
	if user.Status != model.AdminStatusEnabled {
		return ErrAdminUserDisabled
	}

	valid, _, err := security.VerifyPassword(user.PasswordHash, current)
	if err != nil {
		log.Printf("admin password change: stored hash for %s cannot be parsed: %v", user.Username, err)
		return ErrAdminInternal
	}
	if !valid {
		return ErrAdminCredentialsInvalid
	}
	if err := validateAdminPassword(user.Username, next); err != nil {
		return err
	}
	if current == next {
		return ErrAdminPasswordUnchanged
	}

	hash, err := security.HashPassword(next)
	if err != nil {
		return ErrAdminInternal
	}
	// Bumps auth_version, which invalidates every access token in flight.
	if err := a.store.UpdateAdminUserPassword(ctx, userID, hash); err != nil {
		return ErrAdminInternal
	}
	if err := a.store.RevokeSessionsForUser(ctx, userID, revokeReasonPassword); err != nil {
		return ErrAdminInternal
	}
	return nil
}

// --- password policy -------------------------------------------------------

// validateAdminPassword enforces the password rules. It returns a sentinel so
// the HTTP layer can render a 400 without leaking which rule failed to anyone
// who is not already authenticated.
func validateAdminPassword(username, password string) error {
	if password == "" {
		return ErrAdminPasswordWeak
	}
	length := utf8.RuneCountInString(password)
	if length < MinAdminPasswordLength || length > MaxAdminPasswordLength {
		return ErrAdminPasswordWeak
	}
	if strings.EqualFold(strings.TrimSpace(username), strings.TrimSpace(password)) {
		return ErrAdminPasswordWeak
	}
	// Require at least two character classes: letters, digits, symbols. A
	// long passphrase of one class is still rejected, which is what the campus
	// deployment asked for.
	classes := 0
	var hasLetter, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsSpace(r):
			// Whitespace neither counts as a class nor disqualifies.
		default:
			hasSymbol = true
		}
	}
	for _, ok := range []bool{hasLetter, hasDigit, hasSymbol} {
		if ok {
			classes++
		}
	}
	if classes < 2 {
		return ErrAdminPasswordWeak
	}
	return nil
}

// filterAdminRoles keeps only known roles and returns them as a set.
//
// An account with no recognisable role becomes a plain member, never an
// auditor. "auditor" grants read access to request logs and audit trails, so
// quietly promoting an unlabelled account into it would hand out visibility
// nobody granted. The trusted-header path has its own fallback and is not
// affected: a campus proxy that sends no group still means "auditor" there.
func filterAdminRoles(roles []string) map[string]bool {
	return roleSet(roles)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
