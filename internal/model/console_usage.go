package model

// Personal usage is deliberately its own projection rather than a reuse of the
// administrator's RequestLogUsage. The two answer different questions: the
// operator's view groups by API key and quotes a tenant quota against a filter
// they chose, while the member's view is fixed to their own owner snapshot and
// groups by the model they called. Keeping the types apart stops one ticket's
// change from silently reshaping the other's page.

// ConsoleUsageTotals is the member's own traffic for one period. Successes and
// failures are disjoint and always add up to Requests; error_rate is the
// percentage of failures, so 0 means "no failures", never "not measured".
type ConsoleUsageTotals struct {
	Requests     int64   `json:"requests"`
	Successes    int64   `json:"successes"`
	Failures     int64   `json:"failures"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	ErrorRate    float64 `json:"error_rate"`
}

// ConsoleUsageModel is one model's share of the member's traffic. Alias is the
// requested Virtual Model alias from the Request Record, which is the name the
// member sent and therefore the name they can act on.
type ConsoleUsageModel struct {
	Alias        string `json:"alias"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// ConsoleUsageQuota is the Tenant's shared budget as a read-only share. It
// carries aggregate bands only — no member, key or request detail — so a member
// can see that the budget is shared without learning who else spent it.
//
// A band is nil when the Tenant has no configured limit for it, matching the
// administrator usage endpoint's convention. That "not set" state is different
// from 0%, and the console renders it as such.
type ConsoleUsageQuota struct {
	TenantID       string          `json:"tenant_id"`
	TenantName     string          `json:"tenant_name"`
	DailyToken     *QuotaDimension `json:"daily_token"`
	MonthlyToken   *QuotaDimension `json:"monthly_token"`
	MonthlyRequest *QuotaDimension `json:"monthly_request"`
}

// ConsoleUsage is the personal usage page's whole response.
//
// The series is the same RequestLogBucket shape the administrator trend uses,
// because the page reuses the same chart and the same gap-filling rules. From
// and To are echoed back so the page can state the exact window it rendered
// rather than assuming its picker was honoured.
//
// CostAvailable is explicit and always false: the gateway records tokens, not
// prices, so the page must say cost is unavailable instead of deriving a
// currency figure from tokens.
type ConsoleUsage struct {
	From          string              `json:"from"`
	To            string              `json:"to"`
	Bucket        string              `json:"bucket"`
	Totals        ConsoleUsageTotals  `json:"totals"`
	Series        []RequestLogBucket  `json:"series"`
	ByModel       []ConsoleUsageModel `json:"by_model"`
	TenantQuota   *ConsoleUsageQuota  `json:"tenant_quota"`
	CostAvailable bool                `json:"cost_available"`
}
