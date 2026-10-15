package app

// Codex Round 2 — P1-04 adversarial regression tests.
//
// The finding: a caller who learned a real session id (from an access-token
// claim, a device list or a log line) could present "<victim_sid>.<anything>"
// for a session whose stored hash it did not match. The server treated every
// such mismatch as a refresh-token replay and retired the account's entire
// session family. Knowing an identifier — not a secret — was enough to force
// every device an operator owns to re-authenticate.
//
// The fix: refresh tokens are signed as "v2.<sid>.<secret>.<mac>" under a
// domain-separated HMAC label, and only a token that proves knowledge of the
// signing secret can trigger a revocation. A forged legacy token and a forged
// v2 MAC both fail closed as 401 with no side effect, while a genuine replay of
// a signed token still retires the family.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// forgeLegacyToken builds the exact forgery the finding describes: a real
// session id paired with a secret the attacker made up.
func forgeLegacyToken(sid string) string {
	return sid + "." + strings.Repeat("a", 43)
}

// forgeV2Token builds the same forgery wearing the v2 envelope. The attacker
// can copy the prefix; without the signing secret they cannot produce a MAC
// that verifies.
func forgeV2Token(sid string) string {
	return "v2." + sid + "." + strings.Repeat("b", 43) + "." + strings.Repeat("c", 43)
}

// TestRefreshForgedLegacyTokenDoesNotRevokeFamily pins the core of the fix: a
// legacy-shaped "<sid>.<guess>" for a real session must be refused with 401 and
// must not touch any session — in particular it must not be recorded as refresh
// reuse, which is what used to retire the whole family.
func TestRefreshForgedLegacyTokenDoesNotRevokeFamily(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	// Two devices, so a family revocation would be unmistakably observable.
	a, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	b, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")
	if err != nil {
		t.Fatalf("login B: %v", err)
	}
	if store.liveSessions() != 2 {
		t.Fatalf("expected two live sessions, got %d", store.liveSessions())
	}

	forged := forgeLegacyToken(a.Session.SID)
	if _, err := svc.refresh(context.Background(), forged, "10.0.0.9", ""); !errors.Is(err, ErrAdminUnauthenticated) {
		t.Fatalf("a forged legacy token must be rejected as unauthenticated, got %v", err)
	}
	if store.liveSessions() != 2 {
		t.Fatalf("a forged token must revoke nothing, %d sessions still live", store.liveSessions())
	}
	if n := store.revokeCallsFor(a.User.ID); n != 0 {
		t.Fatalf("a forged token must not revoke the session family, got %d revocations", n)
	}
	if store.lastRevokeReason(a.User.ID) == revokeReasonReuse {
		t.Fatal("a forged token must never be recorded as refresh reuse")
	}

	// The victim's own sessions must be untouched and still usable.
	if _, err := svc.refresh(context.Background(), b.RefreshToken, "10.0.0.6", ""); err != nil {
		t.Fatalf("the victim's other device must still refresh: %v", err)
	}
}

// TestRefreshForgedV2MACDoesNotRevokeFamily covers the same attack wearing the
// signed envelope. A MAC that does not verify means the bearer did not know the
// signing secret, so the token proves nothing and revokes nothing.
func TestRefreshForgedV2MACDoesNotRevokeFamily(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	a, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	b, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", "")
	if err != nil {
		t.Fatalf("login B: %v", err)
	}
	if store.liveSessions() != 2 {
		t.Fatalf("expected two live sessions, got %d", store.liveSessions())
	}

	forged := forgeV2Token(a.Session.SID)
	if _, err := svc.refresh(context.Background(), forged, "10.0.0.9", ""); !errors.Is(err, ErrAdminUnauthenticated) {
		t.Fatalf("a v2 token with a bad MAC must be rejected as unauthenticated, got %v", err)
	}
	if store.liveSessions() != 2 {
		t.Fatalf("a forged MAC must revoke nothing, %d sessions still live", store.liveSessions())
	}
	if n := store.revokeCallsFor(a.User.ID); n != 0 {
		t.Fatalf("a forged MAC must not revoke the session family, got %d revocations", n)
	}
	if _, err := svc.refresh(context.Background(), b.RefreshToken, "10.0.0.6", ""); err != nil {
		t.Fatalf("the victim's other device must still refresh: %v", err)
	}
}

// TestRefreshRepeatedForgeryNeverRevokes proves the attacker cannot escalate by
// sheer repetition: knowing the sid lets them forge forever, and every attempt
// must be a no-op rather than eventually tripping the revocation path.
func TestRefreshRepeatedForgeryNeverRevokes(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	sid := tokens.Session.SID

	for i := 0; i < 25; i++ {
		forged := sid + "." + strings.Repeat("x", 43)
		if _, err := svc.refresh(context.Background(), forged, "10.0.0.9", ""); !errors.Is(err, ErrAdminUnauthenticated) {
			t.Fatalf("attempt %d: expected ErrAdminUnauthenticated, got %v", i, err)
		}
		if _, err := svc.refresh(context.Background(), forgeV2Token(sid), "10.0.0.9", ""); !errors.Is(err, ErrAdminUnauthenticated) {
			t.Fatalf("attempt %d (v2): expected ErrAdminUnauthenticated, got %v", i, err)
		}
	}
	if store.liveSessions() != 1 {
		t.Fatalf("repeated forgery must leave the session alive, %d live", store.liveSessions())
	}
	if n := store.revokeCallsFor(tokens.User.ID); n != 0 {
		t.Fatalf("repeated forgery must never revoke, got %d revocations", n)
	}
	// The legitimate token still works.
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("the real token must still rotate after forgery attempts: %v", err)
	}
}

// TestRefreshSignedReplayStillRevokesFamily is the other half of the invariant:
// the fix must not disarm replay detection. A genuine signed token that is
// copied and replayed outside the grace window still retires every session the
// account owns.
func TestRefreshSignedReplayStillRevokesFamily(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", ""); err != nil {
		t.Fatalf("login B: %v", err)
	}

	rotated, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !strings.HasPrefix(rotated.RefreshToken, "v2.") {
		t.Fatalf("rotation must issue a signed v2 token, got %q", rotated.RefreshToken)
	}

	// Past the grace window the superseded token is a replay of a token the
	// server itself signed, so the whole family goes.
	base := time.Now()
	svc.now = func() time.Time { return base.Add(testConfig().AdminRefreshReplayGrace + time.Second) }
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("a signed token replayed outside the grace window must revoke, got %v", err)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("a genuine replay must retire every session, %d still live", store.liveSessions())
	}
	if store.lastRevokeReason(tokens.User.ID) != revokeReasonReuse {
		t.Fatalf("the replay must be recorded with the reuse reason, got %q", store.lastRevokeReason(tokens.User.ID))
	}
}

// TestRefreshTransparentlyUpgradesLegacyToken covers the compatibility half: a
// deployment whose cookie was minted before the signing change still holds
// "<sid>.<secret>". That token is accepted because the database proves it, and
// the first rotation hands back a signed v2 token without forcing a re-login.
func TestRefreshTransparentlyUpgradesLegacyToken(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	sid := tokens.Session.SID

	// Rewrite the stored hash as if this session predates the signing change.
	legacySecret, err := security.NewRandomID(32)
	if err != nil {
		t.Fatalf("random: %v", err)
	}
	legacyToken := sid + "." + legacySecret
	s := store.sessions[sid]
	s.RefreshHash = security.HashAdminRefreshToken(legacyToken)
	s.PreviousRefreshHash = ""
	s.PreviousRefreshValidUntil = nil
	store.sessions[sid] = s

	out, err := svc.refresh(context.Background(), legacyToken, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("a database-proven legacy token must still rotate: %v", err)
	}
	if !strings.HasPrefix(out.RefreshToken, "v2.") {
		t.Fatalf("the successor of a legacy token must be a signed v2 token, got %q", out.RefreshToken)
	}
	parsed, err := security.ParseAdminRefreshToken(svc.secret, out.RefreshToken)
	if err != nil {
		t.Fatalf("the upgraded token must parse: %v", err)
	}
	if !parsed.Signed {
		t.Fatal("the upgraded token must carry a MAC the server can verify")
	}
	if parsed.SessionID != sid {
		t.Fatalf("the upgrade must keep the same session id, got %q", parsed.SessionID)
	}
	// The upgraded token must itself be rotatable.
	if _, err := svc.refresh(context.Background(), out.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("the upgraded token must rotate: %v", err)
	}
}

// A legacy token in previous_refresh_hash is database-proven even though it has
// no v2 MAC. Inside the grace window it is the ordinary concurrent-refresh
// race; outside the window it is a genuine replay and must retire the family.
func TestRefreshLegacyPreviousHonorsGraceAndThenRevokes(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	deviceA, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login A: %v", err)
	}
	if _, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.6", ""); err != nil {
		t.Fatalf("login B: %v", err)
	}

	legacySecret, err := security.NewRandomID(32)
	if err != nil {
		t.Fatal(err)
	}
	legacy := deviceA.Session.SID + "." + legacySecret
	s := store.sessions[deviceA.Session.SID]
	s.RefreshHash = security.HashAdminRefreshToken(legacy)
	s.PreviousRefreshHash = ""
	s.PreviousRefreshValidUntil = nil
	store.sessions[s.SID] = s

	base := time.Now()
	svc.now = func() time.Time { return base }
	successor, err := svc.refresh(context.Background(), legacy, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("legacy current refresh: %v", err)
	}
	if !strings.HasPrefix(successor.RefreshToken, "v2.") {
		t.Fatalf("legacy successor must be v2, got %q", successor.RefreshToken)
	}

	svc.now = func() time.Time { return base.Add(svc.grace) }
	benign, err := svc.refresh(context.Background(), legacy, "10.0.0.5", "")
	if err != nil {
		t.Fatalf("legacy previous at grace boundary must be benign: %v", err)
	}
	if benign.RefreshToken != successor.RefreshToken {
		t.Fatal("legacy previous must converge on the same v2 successor")
	}
	if store.liveSessions() != 2 {
		t.Fatal("legacy previous inside grace must not revoke the family")
	}

	svc.now = func() time.Time { return base.Add(svc.grace + time.Nanosecond) }
	if _, err := svc.refresh(context.Background(), legacy, "10.0.0.5", ""); !errors.Is(err, ErrAdminRefreshReplay) {
		t.Fatalf("database-proven legacy previous outside grace must be replay, got %v", err)
	}
	if store.liveSessions() != 0 || store.revokeCallsFor(deviceA.User.ID) != 1 {
		t.Fatalf("legacy replay must revoke the family once: live=%d revokes=%d",
			store.liveSessions(), store.revokeCallsFor(deviceA.User.ID))
	}
}

// Once a legacy token is older than previous_refresh_hash the database no
// longer carries proof that it was issued. It must be rejected without a
// family-wide side effect; only v2 generations retain authenticity through the
// MAC after falling out of the previous slot.
func TestRefreshLegacyOlderThanPreviousRejectsWithoutRevoke(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	device, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatal(err)
	}
	legacySecret, err := security.NewRandomID(32)
	if err != nil {
		t.Fatal(err)
	}
	legacy := device.Session.SID + "." + legacySecret
	s := store.sessions[device.Session.SID]
	s.RefreshHash = security.HashAdminRefreshToken(legacy)
	s.PreviousRefreshHash = ""
	s.PreviousRefreshValidUntil = nil
	store.sessions[s.SID] = s

	first, err := svc.refresh(context.Background(), legacy, "10.0.0.5", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.refresh(context.Background(), first.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.refresh(context.Background(), legacy, "10.0.0.5", ""); !errors.Is(err, ErrAdminUnauthenticated) {
		t.Fatalf("unprovable old legacy token must be unauthenticated, got %v", err)
	}
	if store.revokeCallsFor(device.User.ID) != 0 || store.liveSessions() != 1 {
		t.Fatal("unprovable old legacy token must not revoke a session")
	}
}

// TestRefreshRevocationFailureIsReportedHonestly pins that a replay whose
// revocation could not be carried out is not reported as a successful replay.
// Claiming the family was retired when it was not would put a false security
// event in the audit trail and leave the operator trusting a response that
// describes something that never happened.
func TestRefreshRevocationFailureIsReportedHonestly(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	base := time.Now()
	svc.now = func() time.Time { return base.Add(testConfig().AdminRefreshReplayGrace + time.Second) }
	store.revokeSessionsErr = errors.New("session store unavailable")

	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminInternal) {
		t.Fatalf("a failed family revocation must surface as ErrAdminInternal, not a successful replay, got %v", err)
	}
	// The session family was never actually retired, so nothing may be recorded
	// as revoked.
	if store.liveSessions() != 1 {
		t.Fatalf("a failed revocation must not be recorded as one, %d live", store.liveSessions())
	}
}

// TestRefreshMACIsDomainSeparated guards against the cheap mistake of reusing
// the rotation label for the integrity tag: the two derivations must not agree,
// or a forged MAC could be replayed as a rotation secret and vice versa.
func TestRefreshMACIsDomainSeparated(t *testing.T) {
	secret := []byte("unit-test-signing-secret")
	if security.AdminRefreshRotationLabel == "" {
		t.Fatal("rotation label must be set")
	}
	// Same inputs, different purposes: the outputs must differ. If the labels
	// were ever collapsed these would collide.
	rotation, _, err := security.DeriveNextAdminRefreshToken(secret, "sid-1", "secret-1")
	if err != nil {
		t.Fatal(err)
	}
	minted, _, err := security.NewAdminRefreshToken(secret, "sid-1")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := security.ParseAdminRefreshToken(secret, rotation)
	if err != nil {
		t.Fatal(err)
	}
	mintTok, err := security.ParseAdminRefreshToken(secret, minted)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Secret == mintTok.Secret {
		t.Fatal("the rotation derivation and the minted secret must not agree")
	}
	// A token signed with one secret must not verify under another.
	if _, err := security.ParseAdminRefreshToken([]byte("a-different-secret"), minted); err == nil {
		t.Fatal("a token must not verify under a different signing secret")
	}
}

// TestLogoutWithForgedTokenRevokesNothing keeps the sibling endpoint honest: a
// forged token must not be able to sign another operator out either, even though
// logout is idempotent by design.
func TestLogoutWithForgedTokenRevokesNothing(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	sid := tokens.Session.SID

	for _, forged := range []string{forgeLegacyToken(sid), forgeV2Token(sid)} {
		ok, err := svc.logout(context.Background(), forged)
		if err != nil {
			t.Fatalf("logout with %q must still be a clean exit: %v", forged, err)
		}
		if ok {
			t.Fatalf("logout with %q must not revoke anything", forged)
		}
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a forged token must not sign the operator out, %d live", store.liveSessions())
	}
	// The legitimate token still logs the operator out.
	ok, err := svc.logout(context.Background(), tokens.RefreshToken)
	if err != nil || !ok {
		t.Fatalf("the real token must log the operator out: ok=%v err=%v", ok, err)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("the real token must revoke the session, %d live", store.liveSessions())
	}
}
