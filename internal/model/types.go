package model

import "time"

// AllModelsWildcard is the allowed_models entry that grants every model.
//
// It exists so that the empty list can mean "nothing". The console told
// operators "不勾选则不允许调用任何对外模型" while the gateway read an empty
// list as "no restriction", and a security default must not depend on which of
// two conventions the reader happens to assume. Naming the unrestricted case
// explicitly makes an empty list unambiguous, and makes "all models" something
// an operator has to choose rather than something they get by omission.
const AllModelsWildcard = "*"

type APIKey struct {
	ID             string
	TenantID       string
	Name           string
	KeyPrefix      string
	KeyDigest      string
	Status         int
	ExpireAt       *time.Time
	AllowedModels  []string
	AllowedIPs     []string
	RPMLimit       int64
	TPMLimit       int64
	MaxConcurrency int64
	RouteGroup     string
	// TenantStatus is the owning tenant's status. It is read here because a
	// key may not serve traffic for a tenant that has been switched off, and
	// the key row on its own cannot answer that question.
	TenantStatus        int
	DailyTokenLimit     int64
	MonthlyTokenLimit   int64
	MonthlyRequestLimit int64
}

// Enabled reports whether the key itself may serve traffic, independent of the
// tenant that owns it. Like the tenant's status it is 1 or 2, so the zero value
// is a third case again, and the same rule applies: grant on one value only.
func (k APIKey) Enabled() bool {
	return k.Status == 1
}

// TenantEnabled reports whether the key's owning tenant may serve traffic.
//
// Status is stored as 1 for enabled and 2 for disabled, which leaves the zero
// value meaning neither: a key row read without its tenant (or from a query
// that forgot to select the tenant) has status 0. Answering that case with
// "enabled" is precisely how disabling a tenant stayed a label with no effect,
// so the method grants on one value and refuses everything else, including the
// one it cannot identify.
func (k APIKey) TenantEnabled() bool {
	return k.TenantStatus == 1
}

type Identity struct {
	TenantID      string
	APIKeyID      string
	AllowedModels map[string]struct{}
	// AllModels reports that the key was granted the wildcard. It is kept
	// separate from AllowedModels so that "unrestricted" and "restricted to
	// nothing" cannot collapse into the same empty map.
	AllModels      bool
	AllowedIPs     []string
	RPMLimit       int64
	TPMLimit       int64
	MaxConcurrency int64
	RouteGroup     string
	DailyTokenLimit     int64
	MonthlyTokenLimit   int64
	MonthlyRequestLimit int64
}

// AllowsModel reports whether this identity may call the named model.
//
// The rule lives on the type rather than at the call site because this is the
// one place where "unrestricted" and "restricted to nothing" could be confused:
// an empty AllowedModels map with AllModels false must deny, and only the
// wildcard flag may grant everything. A caller that instead tested
// len(AllowedModels) > 0 would silently reintroduce the inverted default this
// type exists to remove.
func (i Identity) AllowsModel(name string) bool {
	if i.AllModels {
		return true
	}
	_, ok := i.AllowedModels[name]
	return ok
}

type RequestLog struct {
	RequestID      string
	TenantID       string
	APIKeyID       string
	ClientIP       string
	RequestedModel string
	ActualModel    string
	Provider       string
	UpstreamModel  string
	RoutePriority  int
	Attempts       int
	Failover       bool
	Stream         bool
	InputTokens    int64
	OutputTokens   int64
	TotalTokens    int64
	LatencyMS      int64
	StatusCode     int
	ErrorCode      string
}

// QuotaUsageRow is the settled usage for one tenant on one UTC day, derived
// from the request log (the durable truth of the system).
//
// It is the input a Quota Counter rebuild writes back to Redis: the day bucket
// is formatted exactly like the limiter's day key ("YYYYMMDD" in UTC), so the
// reconstructed counters land on the same keys the gate reads. Month buckets
// are the day bucket's first six characters ("YYYYMM"), matching the limiter's
// month keys. In-flight usage — reserved on the counter but not yet written to
// the log — is deliberately absent here, so rebuilding never fabricates a
// charge that has not settled.
type QuotaUsageRow struct {
	TenantID string // tenant id as text; the tenant segment of the Redis key
	Day      string // UTC day bucket "YYYYMMDD", == limiter tenantDayBucket()
	Requests int64  // settled request count for that day
	Tokens   int64  // settled total_tokens for that day
}
