package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// adminUserView is the safe projection of an account for the user management
// page.
//
// It deliberately has no password_hash, no refresh hash and no auth_version.
// The console needs to show who an account is and what it may do; nothing about
// how it authenticates belongs in a response that a browser will hold in memory
// and a proxy may log.
type adminUserView struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Status      int      `json:"status"`
	// TenantID and TenantName are the Account's Tenant Membership. Empty
	// strings mean "unassigned", not a Tenant named "". The name is projected
	// for display; the id is the identifier.
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	// PasswordResetRequired tells the user management page that this account is
	// still holding a password an operator handed over, so the operator can see
	// that the handover was never completed rather than assuming it was.
	PasswordResetRequired bool    `json:"password_reset_required"`
	LastLoginAt           *string `json:"last_login_at"`
	CreatedAt             string  `json:"created_at"`
}

func adminUserViewFrom(u model.AdminUser) adminUserView {
	var lastLogin *string
	if u.LastLoginAt != nil {
		formatted := u.LastLoginAt.UTC().Format(time.RFC3339)
		lastLogin = &formatted
	}
	return adminUserView{
		ID:                    u.ID,
		Username:              u.Username,
		DisplayName:           u.DisplayName,
		Roles:                 NormalizeAccountRoles(u.Roles),
		Status:                u.Status,
		TenantID:              u.TenantID,
		TenantName:            u.TenantName,
		PasswordResetRequired: u.PasswordResetRequired,
		LastLoginAt:           lastLogin,
		CreatedAt:             u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// --- service ---------------------------------------------------------------

// userUpdate is a partial change. A nil field means "leave it alone", which is
// what lets the UI send only what the operator actually touched.
type userUpdate struct {
	DisplayName *string
	Roles       *[]string
	Status      *int
}

// listUsers returns every account plus the summary counts.
func (a *adminAuthService) listUsers(ctx context.Context) ([]model.AdminUser, model.AdminUserStats, error) {
	users, err := a.store.ListAdminUsers(ctx, 500)
	if err != nil {
		return nil, model.AdminUserStats{}, ErrAdminInternal
	}
	stats, err := a.store.AdminUserStats(ctx)
	if err != nil {
		return nil, model.AdminUserStats{}, ErrAdminInternal
	}
	return users, stats, nil
}

// updateUser applies a role, status or display-name change on behalf of a super
// administrator, and reports what actually changed.
//
// The change is one storage call, and therefore one transaction. Applying roles,
// status and display name as three separate mutations meant a patch that was
// rejected for the super-administrator invariant could still leave the display
// name written, and a patch that touched roles and status together bumped
// auth_version twice for what is one logical change.
//
// Validation happens here rather than in storage because it produces an HTTP
// 400, and storage should not be deciding request semantics. Normalisation is
// idempotent, so running it on this side of the boundary changes nothing about
// what is stored.
//
// Changing your own account is allowed. The invariant already prevents the only
// genuinely dangerous case, and an operator who deliberately steps down from
// super_admin should be able to: their own sessions are revoked by the same
// change, so the console signs them out immediately afterwards.
func (a *adminAuthService) updateUser(ctx context.Context, targetID string, in userUpdate) (model.AdminUser, storage.AdminUserChangeSet, error) {
	mutation := storage.AdminUserMutation{}

	if in.Roles != nil {
		// Unknown roles are refused rather than dropped: an operator who sends
		// ["gateway_admin","god_mode"] should be told the request was not
		// understood, not handed an apparent success that quietly discarded
		// half of it.
		if err := ValidateAccountRoles(*in.Roles); err != nil {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, err
		}
		roles := NormalizeAccountRoles(*in.Roles)
		mutation.Roles = &roles
	}

	if in.Status != nil {
		if *in.Status != model.AdminStatusEnabled && *in.Status != model.AdminStatusDisabled {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminBadRequest
		}
		status := *in.Status
		mutation.Status = &status
	}

	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if utf8.RuneCountInString(name) > maxDisplayNameRunes {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminBadRequest
		}
		mutation.DisplayName = &name
	}

	updated, changes, err := a.store.UpdateAdminUserAccount(ctx, targetID, mutation)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrLastSuperAdmin):
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminLastSuperAdmin
		case errors.Is(err, storage.ErrAdminUserNotFound):
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminUserNotFound
		default:
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminInternal
		}
	}

	// A display-name-only edit is not an authentication event: nobody is signed
	// out and no token is invalidated.
	if !changes.AuthenticationChanged() {
		return updated, changes, nil
	}

	// Exactly one revocation per logical mutation. Revoking after each field
	// would do the same work twice for a combined patch and would leave the
	// second reason in the audit trail as though it were a separate event.
	//
	// This is best effort: the transaction has already bumped auth_version, so
	// every token issued before the change is rejected on its next use whether
	// or not the session rows were tidied up. Failing here must not undo a
	// permission change that has already committed.
	if err := a.store.RevokeSessionsForUser(ctx, targetID, revokeReasonForChange(changes)); err != nil {
		log.Printf("user management: account %s changed but its sessions could not be revoked: %v", targetID, err)
	}
	return updated, changes, nil
}

// createUser lets an administrator open an account for somebody who has not
// registered, choose its roles, and hand over a password.
//
// It differs from public registration in three ways, each deliberate:
//
//   - The role list is a parameter. Registration is hard-wired to ["member"]
//     because an anonymous caller must not choose its own authority; here the
//     caller is a super administrator, and the roles they pick are the grant.
//   - No registration throttle applies. That counter exists to bound anonymous
//     account creation, and the operator is not anonymous.
//   - The password is marked reset-required, exactly as an administrator reset
//     is. The operator chose it and can read it, so it proves a handover, not
//     an identity; the account must choose its own before it can do anything.
//
// A Tenant may be assigned in the same call. It is written after the account
// exists because the row has to exist first, and a failure there is reported as
// a partial success rather than undoing an account the operator asked for.
func (a *adminAuthService) createUser(ctx context.Context, in adminUserCreate) (model.AdminUser, error) {
	username := strings.TrimSpace(in.Username)
	displayName := strings.TrimSpace(in.DisplayName)

	if username == "" || utf8.RuneCountInString(username) > 64 {
		return model.AdminUser{}, ErrAdminBadRequest
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameRunes {
		return model.AdminUser{}, ErrAdminBadRequest
	}
	// The same role rules the account patch applies. "member" is derived, not
	// chosen: NormalizeAccountRoles keeps it only when no management role was
	// picked, which is what "an ordinary account" means.
	if err := ValidateAccountRoles(in.Roles); err != nil {
		return model.AdminUser{}, err
	}
	roles := NormalizeAccountRoles(in.Roles)
	if err := validateAdminPassword(username, in.Password); err != nil {
		return model.AdminUser{}, err
	}
	if in.TenantID != "" {
		// Checked before the account is inserted so a mistyped Tenant does not
		// leave an orphan account the operator has to find and delete.
		//
		// A missing Tenant answers (nil, nil) from the allowlist read, which is
		// the same answer as "a Tenant that grants nothing", so existence is
		// asked for directly rather than inferred from that read.
		exists, err := a.store.TenantExists(ctx, in.TenantID)
		if err != nil {
			return model.AdminUser{}, ErrAdminInternal
		}
		if !exists {
			return model.AdminUser{}, ErrAdminTenantNotFound
		}
	}

	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return model.AdminUser{}, ErrAdminInternal
	}
	created, err := a.store.CreateUser(ctx, username, displayName, hash, roles)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrAdminUsernameTaken):
			return model.AdminUser{}, ErrAdminUsernameTaken
		case errors.Is(err, storage.ErrAdminUserNotFound):
			return model.AdminUser{}, ErrAdminInternal
		default:
			return model.AdminUser{}, ErrAdminInternal
		}
	}

	// The reset flag is what makes the handed-over password temporary. It is
	// set with the same statement shape a reset uses, so the console shows the
	// account as "waiting for its owner" from the moment it exists.
	if _, err := a.store.ResetAdminUserPassword(ctx, created.ID, hash); err != nil {
		log.Printf("user management: account %s created but could not be marked reset-required: %v", created.Username, err)
	}

	if in.TenantID != "" {
		assigned, err := a.store.AssignAdminUserTenant(ctx, created.ID, in.TenantID)
		if err != nil {
			log.Printf("user management: account %s created but joining Tenant %s failed, an operator must assign it: %v",
				created.Username, in.TenantID, err)
			created.TenantID = ""
			created.TenantName = ""
			return created, nil
		}
		return assigned, nil
	}
	updated, err := a.store.FindAdminUserByID(ctx, created.ID)
	if err != nil {
		return created, nil
	}
	return updated, nil
}

// adminUserCreate is the validated input for an administrator-created account.
type adminUserCreate struct {
	Username    string
	DisplayName string
	Password    string
	Roles       []string
	TenantID    string
}

// adminUserOwnsKeysError is the service-level form of the storage refusal, kept
// so the HTTP layer never has to know which layer decided the count.
type adminUserOwnsKeysError struct{ Count int64 }

func (e *adminUserOwnsKeysError) Error() string {
	return fmt.Sprintf("the account still owns %d API key(s)", e.Count)
}

// assignUserTenant sets or clears an account's Tenant Membership.
//
// Unlike a role or status change this does not revoke sessions: membership is
// not part of the access token's authority, and every personal endpoint reads
// it from the account row. Signing the account out would be a punishment with
// no security value.
//
// The "still owns keys" refusal is passed through unchanged because it carries
// the count the console reports. Everything the storage layer can answer is
// translated to a sentinel here, so the handler's mapping stays exhaustive.
func (a *adminAuthService) assignUserTenant(ctx context.Context, targetID, tenantID string) (model.AdminUser, error) {
	updated, err := a.store.AssignAdminUserTenant(ctx, targetID, tenantID)
	if err == nil {
		return updated, nil
	}

	var ownsKeys *storage.AdminUserOwnsKeysError
	switch {
	case errors.As(err, &ownsKeys):
		return model.AdminUser{}, &adminUserOwnsKeysError{Count: ownsKeys.Count}
	case errors.Is(err, storage.ErrAdminUserNotFound):
		return model.AdminUser{}, ErrAdminUserNotFound
	case errors.Is(err, storage.ErrAdminTenantNotFound):
		return model.AdminUser{}, ErrAdminTenantNotFound
	default:
		return model.AdminUser{}, ErrAdminInternal
	}
}

// revokeReasonForChange names the reason recorded on the revoked sessions, so
// the device list and the audit trail say what actually happened rather than
// lumping every kind of change under one label.
func revokeReasonForChange(c storage.AdminUserChangeSet) string {
	switch {
	case c.RolesChanged && c.StatusChanged:
		return revokeReasonSecurityChanged
	case c.RolesChanged:
		return revokeReasonRoleChanged
	case c.NewStatus == model.AdminStatusDisabled:
		return revokeReasonDisabled
	default:
		return revokeReasonStatusChanged
	}
}

// --- handlers --------------------------------------------------------------

// handleAdminUsers serves GET /admin/users.
//
// Only super_admin reaches this handler: the RBAC allow-list in adminPrincipal
// grants no other role the /admin/users prefix. That matters more here than
// anywhere else — gateway_admin having user management would be able to promote
// itself to super_admin, which is the whole reason the prefix is not shared.
//
// The mode check matters as much as the role check: in token and trusted-header
// mode there is no account service behind this server, so reaching for it would
// panic rather than refuse.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	// The collection serves two operations: reading the list, and opening a new
	// account. They share the RBAC prefix — /admin/users is super_admin only —
	// so the split is by method rather than by path.
	if r.Method == http.MethodPost {
		s.handleAdminUserCreate(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	users, stats, err := s.adminAuth.listUsers(r.Context())
	if err != nil {
		writeAdminError(w, reqID, err)
		return
	}
	views := make([]adminUserView, 0, len(users))
	for _, u := range users {
		views = append(views, adminUserViewFrom(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": views,
		"stats": map[string]int64{
			"total":    stats.Total,
			"members":  stats.Members,
			"admins":   stats.Admins,
			"disabled": stats.Disabled,
		},
	})
}

// --- POST /admin/users -----------------------------------------------------

type adminUserCreateRequest struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Password    string   `json:"password"`
	Roles       []string `json:"roles"`
	TenantID    string   `json:"tenant_id"`
}

// handleAdminUserCreate opens an account on somebody's behalf.
//
// It is the counterpart to the account patch: the patch changes an account that
// exists, this one makes one. The alternative — asking every new person to
// register themselves and then promoting them — puts the account-creation
// moment in the hands of whoever reaches the site first, and does not work at
// all when public registration is closed.
//
// The response deliberately contains no credential. The operator already knows
// the password they typed; repeating it here would put it in the browser's
// network log, the audit row and any proxy in between.
func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.Username == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	var in adminUserCreateRequest
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	// An omitted role list means "an ordinary account", which is what the
	// normaliser derives from an empty list. Management roles are only ever
	// present because the operator picked them.
	created, err := s.adminAuth.createUser(r.Context(), adminUserCreate{
		Username:    in.Username,
		DisplayName: in.DisplayName,
		Password:    in.Password,
		Roles:       in.Roles,
		TenantID:    strings.TrimSpace(in.TenantID),
	})
	if err != nil {
		s.auditAdminAuth(r, "user.create_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	s.auditAdminAuth(r, "user.create", http.StatusCreated, principal.Username)
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": adminUserViewFrom(created),
		"message": "Account created. Hand the password over out of band: " +
			"the account must choose its own before it can be used.",
	})
}

type adminUserUpdateRequest struct {
	DisplayName *string   `json:"display_name"`
	Roles       *[]string `json:"roles"`
	Status      *int      `json:"status"`
}

// handleAdminUserByID serves PATCH /admin/users/{id} and routes
// PUT /admin/users/{id}/tenant to its own handler.
func (s *Server) handleAdminUserByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	// Same guard as the list handler: without an account service there is
	// nothing to update, and the request must be refused rather than dereference
	// a nil service.
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	targetID := strings.TrimPrefix(r.URL.Path, "/admin/users/")
	// Membership is a separate mutation with its own refusal, so it lives on
	// its own path rather than inside the account patch. Keep the suffix check
	// before the id check below, which would otherwise read "tenant" as part of
	// the id.
	if tenantID, ok := strings.CutSuffix(targetID, "/tenant"); ok {
		if tenantID == "" || strings.Contains(tenantID, "/") {
			http.NotFound(w, r)
			return
		}
		s.handleAdminUserTenant(w, r, tenantID, reqID, principal)
		return
	}
	// The password reset is the same shape of mutation: its own path, its own
	// permission question and its own refusals.
	if resetID, ok := strings.CutSuffix(targetID, "/password-reset"); ok {
		if resetID == "" || strings.Contains(resetID, "/") {
			http.NotFound(w, r)
			return
		}
		s.handleAdminUserPasswordReset(w, r, resetID, reqID, principal)
		return
	}
	if targetID == "" || strings.Contains(targetID, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in adminUserUpdateRequest
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	updated, changes, err := s.adminAuth.updateUser(r.Context(), targetID, userUpdate{
		DisplayName: in.DisplayName,
		Roles:       in.Roles,
		Status:      in.Status,
	})
	if err != nil {
		s.auditAdminAuth(r, "user.update_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// The audit trail follows what changed, not which fields were present in the
	// request. "The client sent a roles field" and "the roles changed" are
	// different facts: a console that resends the current values would otherwise
	// fill the trail with events that never happened.
	for _, action := range accountAuditActions(changes) {
		s.auditAdminAuth(r, action, http.StatusOK, principal.Username)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": adminUserViewFrom(updated),
		// The client is told which fields actually moved so it can describe the
		// outcome accurately instead of guessing from what it sent.
		"changes": map[string]bool{
			"roles":          changes.RolesChanged,
			"status":         changes.StatusChanged,
			"display_name":   changes.DisplayNameChanged,
			"authentication": changes.AuthenticationChanged(),
		},
	})
}

type adminUserPasswordResetRequest struct {
	// NewPassword is the password the operator hands over. There is no
	// "current password" field: the whole point of a reset is that the current
	// credential is unavailable to its owner.
	NewPassword string `json:"new_password"`
}

type adminUserTenantUpdateRequest struct {
	// TenantID is a pointer so an explicit null and an omitted field both mean
	// "unassign": the endpoint is a complete statement about membership, not a
	// partial patch.
	TenantID *string `json:"tenant_id"`
}

// handleAdminUserTenant serves PUT /admin/users/{id}/tenant.
//
// The 409 is written by hand rather than through writeAdminError because the
// refusal has to carry the key count as a field. Embedding the number in the
// message alone would make the console parse prose to render it.
func (s *Server) handleAdminUserPasswordReset(w http.ResponseWriter, r *http.Request, targetID, reqID string, principal adminPrincipal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	// The RBAC allow-list admits only super_admin to /admin/users, and this
	// handler inherits that. The explicit check is for the empty-username case
	// the trusted-header paths can produce, where the principal reached the
	// middleware without an account behind it and therefore could not be the
	// target of an audit trail.
	if principal.Username == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	var in adminUserPasswordResetRequest
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	updated, err := s.adminAuth.resetPassword(r.Context(), targetID, in.NewPassword)
	if err != nil {
		s.auditAdminAuth(r, "user.password_reset_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// The audit row records that a reset happened and to which account. It
	// deliberately does not carry the password: the trail is read by people
	// investigating an incident, and a password in it would be both a leak and
	// a target.
	s.auditAdminAuth(r, "user.password_reset", http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": adminUserViewFrom(updated),
		"message": "Password reset. The account must choose a new password at its next sign-in, " +
			"and every existing session has been revoked.",
	})
}

func (s *Server) handleAdminUserTenant(w http.ResponseWriter, r *http.Request, targetID, reqID string, principal adminPrincipal) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in adminUserTenantUpdateRequest
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	tenantID := ""
	if in.TenantID != nil {
		tenantID = strings.TrimSpace(*in.TenantID)
	}

	updated, err := s.adminAuth.assignUserTenant(r.Context(), targetID, tenantID)
	if err != nil {
		var ownsKeys *adminUserOwnsKeysError
		if errors.As(err, &ownsKeys) {
			// The typed refusal is not in the sentinel table, so its audit
			// status is stated here rather than defaulting to 500.
			s.auditAdminAuth(r, "user.tenant.update_failed", http.StatusConflict, principal.Username)
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"code":    "user_owns_keys",
					"message": ownsKeys.Error(),
					// The count is what makes the refusal actionable: an
					// operator learns how many credentials must go first.
					"request_id":      reqID,
					"owned_key_count": ownsKeys.Count,
				},
			})
			return
		}
		s.auditAdminAuth(r, "user.tenant.update_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	s.auditAdminAuth(r, "user.tenant.update", http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, map[string]any{"data": adminUserViewFrom(updated)})
}

// accountAuditActions lists the semantic events a change set produced, in a
// stable order.
//
// It returns a list rather than a single action because one request can be
// several events an operator later wants to find independently: a patch that
// demotes an account and disables it is both a role update and a disable, and
// collapsing that into one row would hide half of what happened.
//
// A no-op returns nothing at all. Recording "role updated" because the request
// happened to contain a roles field would put events in the trail that never
// took place.
func accountAuditActions(changes storage.AdminUserChangeSet) []string {
	actions := make([]string, 0, 3)
	if changes.RolesChanged {
		actions = append(actions, "user.role.update")
	}
	if changes.StatusChanged {
		if changes.NewStatus == model.AdminStatusDisabled {
			actions = append(actions, "user.disable")
		} else {
			actions = append(actions, "user.enable")
		}
	}
	if changes.DisplayNameChanged {
		actions = append(actions, "user.profile.update")
	}
	return actions
}
