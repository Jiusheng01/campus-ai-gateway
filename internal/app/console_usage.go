package app

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// handleConsoleUsage answers GET /api/console/usage.
//
// The owner predicate is not a query parameter: the Account is re-read from the
// session and its id is bound into every aggregate. tenant_id and api_key_id
// are deliberately not parsed at all, so a caller cannot widen the set by
// naming them — they are ignored, and the endpoint's tests assert that ignoring
// them produces the same numbers as omitting them.
//
// The period is the caller's: from/to are RFC 3339 instants and bucket is hour
// or day, exactly like the administrator trend endpoint. Defaults are the last
// 24 hours in hourly buckets, matching the dashboard's opening view.
func (s *Server) handleConsoleUsage(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	user, ok := s.consoleAccount(w, r)
	if !ok {
		return
	}
	if s.pg == nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}

	q := r.URL.Query()
	unit, unitOK := trendUnit(q.Get("bucket"))
	if !unitOK {
		httpx.Error(w, http.StatusBadRequest, "invalid_bucket", "bucket must be hour or day", reqID)
		return
	}

	to, err := parseTimeParam(q.Get("to"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	if to.IsZero() {
		to = time.Now()
	}
	from, err := parseTimeParam(q.Get("from"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	if !from.Before(to) {
		httpx.Error(w, http.StatusBadRequest, "invalid_range", "from must be earlier than to", reqID)
		return
	}
	if to.Sub(from) > time.Duration(maxTrendBuckets)*trendStep(unit) {
		httpx.Error(w, http.StatusBadRequest, "range_too_wide",
			fmt.Sprintf("the range would need more than %d %s buckets", maxTrendBuckets, unit), reqID)
		return
	}

	offset := parseTrendOffset(q.Get("tz_offset"))

	totals, err := s.pg.PersonalUsageTotals(r.Context(), user.ID, from, to)
	if err != nil {
		log.Printf("console usage: totals for %s: %v", user.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to aggregate usage", reqID)
		return
	}
	byModel, err := s.pg.PersonalUsageByModel(r.Context(), user.ID, from, to)
	if err != nil {
		log.Printf("console usage: by model for %s: %v", user.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to group usage by model", reqID)
		return
	}
	// The owner id is the series' predicate: RequestLogSeries is the same
	// helper the administrator trend uses, with the snapshot bound.
	rows, err := s.pg.RequestLogSeries(r.Context(), user.ID, from, to, unit, offset, trendLabelFormat(unit))
	if err != nil {
		log.Printf("console usage: series for %s: %v", user.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the usage trend", reqID)
		return
	}

	quota, err := s.consoleSharedQuota(r, user.TenantID)
	if err != nil {
		log.Printf("console usage: quota for tenant %s: %v", user.TenantID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the tenant quota", reqID)
		return
	}

	writeJSON(w, http.StatusOK, model.ConsoleUsage{
		From:   from.UTC().Format(time.RFC3339),
		To:     to.UTC().Format(time.RFC3339),
		Bucket: unit,
		Totals: totals,
		// fillTrendBuckets is shared with the administrator dashboard, so a
		// quiet hour is a zero rather than a gap here too.
		Series:  fillTrendBuckets(rows, from, to, unit, offset),
		ByModel: byModel,
		// The quota is the Tenant's whole shared consumption. It is read-only
		// and aggregate: no key or member detail is selected or returned.
		TenantQuota: quota,
		// The gateway records tokens, not money. This is stated rather than
		// implied so no caller can mistake the token figures for a cost.
		CostAvailable: false,
	})
}

// consoleSharedQuota reads the Tenant's aggregate quota bands.
//
// A nil result means the Account has no Tenant Membership (or the membership
// dangles after a Tenant delete). That is an honest empty state on the page,
// not a zero budget: an unassigned Account has no shared quota to be at 0% of.
func (s *Server) consoleSharedQuota(r *http.Request, tenantID string) (*model.ConsoleUsageQuota, error) {
	if tenantID == "" {
		return nil, nil
	}
	tenant, err := s.pg.TenantByID(r.Context(), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stats, err := s.pg.TenantUsageStats(r.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	return &model.ConsoleUsageQuota{
		TenantID:       tenant.ID,
		TenantName:     tenant.Name,
		DailyToken:     quotaUsage(stats.DailyTokens, tenant.DailyTokenLimit),
		MonthlyToken:   quotaUsage(stats.MonthlyTokens, tenant.MonthlyTokenLimit),
		MonthlyRequest: quotaUsage(stats.MonthlyRequests, tenant.MonthlyRequestLimit),
	}, nil
}
