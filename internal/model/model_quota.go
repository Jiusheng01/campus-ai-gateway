package model

// ModelQuota is one row of gateway_model_quota: what a single subject may spend
// on a single model.
//
// The subject is either the whole system (TenantID == nil, the shared pool that
// every tenant's usage also lands on) or one tenant.
//
// Limit fields are pointers so the three states survive the database round
// trip: nil means "inherit", 0 means "unlimited", and a positive value is a cap.
// A plain int64 would collapse "inherit" and "unlimited" into the same number,
// which is exactly the distinction the hierarchy depends on.
type ModelQuota struct {
	// TenantID is nil for the system-wide pool.
	TenantID *string `json:"tenant_id"`
	// TenantCode and TenantName are joined in for the admin listing so the UI
	// does not have to resolve ids against a second list. Empty for the system
	// pool.
	TenantCode string `json:"tenant_code,omitempty"`
	TenantName string `json:"tenant_name,omitempty"`

	ModelAlias string `json:"model_alias"`

	DailyTokenLimit   *int64 `json:"daily_token_limit"`
	MonthlyTokenLimit *int64 `json:"monthly_token_limit"`
}

// ModelQuotaAllocation is the write payload for one (subject, model) pair. It is
// a full replacement, like TenantAllocation: an omitted limit means "inherit",
// which is the same thing as explicitly sending null.
type ModelQuotaAllocation struct {
	TenantID   *string `json:"tenant_id"`
	ModelAlias string  `json:"model_alias"`

	DailyTokenLimit   *int64 `json:"daily_token_limit"`
	MonthlyTokenLimit *int64 `json:"monthly_token_limit"`
}

// ModelQuotaLevel is one node of the resolved model quota chain.
//
// It mirrors TenantQuota in shape but is a separate type on purpose: the two
// chains resolve with the same rule but are built from different sources, and
// keeping them distinct stops a model level from being mistaken for a tenant one
// (they produce different Redis keys and different rejection labels).
//
// The limits are the level's OWN declaration, not an inherited number. The
// reason is the same as for TenantQuota: a counter is inclusive, so the system
// pool's counter already contains every tenant's usage and a tenant's counter
// already contains its children's. Checking an inherited value at each level
// would count the same usage repeatedly; checking only the levels that declare a
// value is both correct and cheaper.
type ModelQuotaLevel struct {
	// Label names this level in a rejection message, e.g.
	// "模型:campus-smart(全校)" or "租户:计算机学院/模型:campus-smart". It is what
	// turns a bare 429 into something an operator can act on.
	Label string
	// TenantID is empty when the level is the system-wide pool: that pool
	// belongs to no tenant, and the security event records it as such.
	TenantID   string
	ModelAlias string

	DailyTokenLimit   *int64
	MonthlyTokenLimit *int64
}

// EffectiveModelQuota is the resolved view of one model for one tenant: what the
// tenant actually gets once inheritance has been applied, and where the value
// came from.
//
// Reporting the source is the point. "I gave the department 300 but it stops at
// 100" is only answerable if the answer can name the level that set the 100 --
// otherwise the administrator has to open a database client to find out.
type EffectiveModelQuota struct {
	ModelAlias string `json:"model_alias"`

	DailyTokenLimit   int64 `json:"daily_token_limit"`
	MonthlyTokenLimit int64 `json:"monthly_token_limit"`

	// SetBy is the label of the level the effective value came from: "全校" for
	// the system pool, otherwise the tenant's name. It names the source of the
	// daily cap, or of the monthly cap when daily is uncapped; the two limits can
	// come from different levels. Empty when no level caps this model.
	SetBy string `json:"set_by"`
	// Own reports whether the effective value was declared by this tenant rather
	// than inherited from above. It is the difference between "I configured this"
	// and "this is what I was given", which is the first thing an operator checks.
	Own bool `json:"own"`
}
