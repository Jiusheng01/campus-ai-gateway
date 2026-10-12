package app

import (
	"errors"
	"net/http"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
)

// Admin authentication sentinel errors.
//
// Every failure path in the authentication layer returns one of these. HTTP
// status codes are derived by errors.Is, never by inspecting error text, so a
// message change can never silently turn a 401 into a 503.
var (
	// ErrAdminAuthDisabled means the active auth mode cannot serve requests:
	// the mode is unknown, or a required setting is missing. It maps to 503
	// because the failure is operational, not a bad credential.
	ErrAdminAuthDisabled = errors.New("admin authentication is not configured")

	// ErrAdminCredentialsInvalid is returned for a wrong username *or* a wrong
	// password. The two are deliberately indistinguishable to the caller.
	ErrAdminCredentialsInvalid = errors.New("username or password is incorrect")

	// ErrAdminUnauthenticated means no usable credential was presented.
	ErrAdminUnauthenticated = errors.New("administrator authentication is required")

	// ErrAdminSessionExpired covers an expired session, a session whose account
	// auth_version moved on, or a session past its absolute lifetime.
	ErrAdminSessionExpired = errors.New("administrator session has expired")

	// ErrAdminSessionRevoked covers an explicitly revoked session, a session
	// version mismatch, and a detected refresh-token replay.
	ErrAdminSessionRevoked = errors.New("administrator session has been revoked")

	// ErrAdminSessionNotFound means the session id in the credential does not
	// exist. Treated as unauthenticated rather than revoked.
	ErrAdminSessionNotFound = errors.New("administrator session was not found")

	// ErrAdminUserDisabled means the account exists but is switched off.
	ErrAdminUserDisabled = errors.New("administrator account is disabled")

	// ErrAdminUserNotFound means the account referenced by a valid session has
	// since been deleted.
	ErrAdminUserNotFound = errors.New("administrator account was not found")

	// ErrAdminForbidden means the caller authenticated but lacks the role the
	// operation requires.
	ErrAdminForbidden = errors.New("administrator role is not permitted for this operation")

	// ErrAdminSetupNotAllowed means first-run setup was attempted after an
	// account already exists.
	ErrAdminSetupNotAllowed = errors.New("an administrator account already exists")

	// ErrAdminSetupRemoteForbidden means first-run setup arrived from a
	// non-loopback address while ADMIN_SETUP_LOCAL_ONLY is on.
	ErrAdminSetupRemoteForbidden = errors.New("initial setup is only allowed from the local machine")

	// ErrAdminRateLimited means too many failed login attempts were recorded in
	// the current window.
	ErrAdminRateLimited = errors.New("too many login attempts, try again later")

	// ErrAdminPasswordWeak means the new password does not meet the policy.
	ErrAdminPasswordWeak = errors.New("password does not meet the security policy")

	// ErrAdminPasswordUnchanged means the new password equals the current one.
	ErrAdminPasswordUnchanged = errors.New("the new password must differ from the current password")

	// ErrAdminCSRFRejected means a cookie-authenticated state-changing request
	// failed the same-origin check.
	ErrAdminCSRFRejected = errors.New("cross-site request rejected")

	// ErrAdminBadRequest means the request body was missing or malformed.
	ErrAdminBadRequest = errors.New("request body is invalid")

	// ErrAdminInternal is the catch-all for unexpected storage failures. The
	// underlying error is logged, never returned to the client.
	ErrAdminInternal = errors.New("administrator authentication failed")
)

// adminErrorStatus maps a sentinel to an HTTP status and a stable machine code.
//
// The mapping is intentionally conservative: anything we cannot classify is a
// 500, never a 503. Reporting "service unavailable" for an authentication
// problem would tell clients to retry something that will never succeed.
func adminErrorStatus(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, ErrAdminAuthDisabled):
		return http.StatusServiceUnavailable, "admin_api_disabled"
	case errors.Is(err, ErrAdminCredentialsInvalid):
		return http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, ErrAdminUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, ErrAdminSessionExpired):
		return http.StatusUnauthorized, "session_expired"
	case errors.Is(err, ErrAdminSessionRevoked):
		return http.StatusUnauthorized, "session_revoked"
	case errors.Is(err, ErrAdminSessionNotFound):
		return http.StatusUnauthorized, "session_not_found"
	case errors.Is(err, ErrAdminUserNotFound):
		return http.StatusUnauthorized, "account_not_found"
	case errors.Is(err, ErrAdminUserDisabled):
		return http.StatusForbidden, "account_disabled"
	case errors.Is(err, ErrAdminForbidden):
		return http.StatusForbidden, "admin_forbidden"
	case errors.Is(err, ErrAdminSetupNotAllowed):
		return http.StatusForbidden, "setup_not_allowed"
	case errors.Is(err, ErrAdminSetupRemoteForbidden):
		return http.StatusForbidden, "setup_required_but_remote_forbidden"
	case errors.Is(err, ErrAdminCSRFRejected):
		return http.StatusForbidden, "csrf_rejected"
	case errors.Is(err, ErrAdminRateLimited):
		return http.StatusTooManyRequests, "too_many_attempts"
	case errors.Is(err, ErrAdminPasswordWeak):
		return http.StatusBadRequest, "password_policy_violation"
	case errors.Is(err, ErrAdminPasswordUnchanged):
		return http.StatusBadRequest, "password_unchanged"
	case errors.Is(err, ErrAdminBadRequest):
		return http.StatusBadRequest, "invalid_request"
	default:
		return http.StatusInternalServerError, "admin_internal_error"
	}
}

// adminErrorMessage returns the client-facing text for a sentinel. Internal
// failures get a generic message so storage details never leak.
func adminErrorMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAdminInternal):
		return ErrAdminInternal.Error()
	default:
		return err.Error()
	}
}

// writeAdminError renders a sentinel as the standard error envelope.
func writeAdminError(w http.ResponseWriter, requestID string, err error) {
	status, code := adminErrorStatus(err)
	httpx.Error(w, status, code, adminErrorMessage(err), requestID)
}
