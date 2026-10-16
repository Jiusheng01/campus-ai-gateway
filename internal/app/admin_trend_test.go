package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// --- bucket arithmetic -----------------------------------------------------

// TestTrendUnitRejectsWhatItCannotDraw pins the two widths the chart knows and
// the fact that anything else is refused rather than silently coerced. A
// "minute" bucket coerced to "hour" would draw a chart the operator did not ask
// for while looking like it worked.
func TestTrendUnitRejectsWhatItCannotDraw(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		unit  string
		valid bool
	}{
		{"", trendUnitHour, true},
		{"hour", trendUnitHour, true},
		{" hour ", trendUnitHour, true},
		{"day", trendUnitDay, true},
		{"minute", "", false},
		{"week", "", false},
		{"HOUR", "", false},
	} {
		unit, ok := trendUnit(tc.raw)
		if ok != tc.valid || unit != tc.unit {
			t.Fatalf("trendUnit(%q) = (%q, %v), want (%q, %v)", tc.raw, unit, ok, tc.unit, tc.valid)
		}
	}
}

// TestTrendLabelFormatsAgree is the contract between the two halves of the
// trend: the database spells the label with to_char, Go regenerates the same
// label to fill the gaps, and the two are matched by string equality. If they
// drifted, every filled bucket would duplicate a real one instead of
// substituting for it.
func TestTrendLabelFormatsAgree(t *testing.T) {
	instant := time.Date(2026, 3, 16, 7, 0, 0, 0, time.UTC)

	if got := instant.Format(trendGoLabelFormat(trendUnitHour)); got != "2026-03-16 07:00" {
		t.Fatalf("hourly Go label = %q, want 2026-03-16 07:00", got)
	}
	if got := instant.Format(trendGoLabelFormat(trendUnitDay)); got != "2026-03-16" {
		t.Fatalf("daily Go label = %q, want 2026-03-16", got)
	}
	// PostgreSQL's to_char reads the same reference characters differently, so
	// the two spellings are asserted against literals rather than against each
	// other: the point is that both describe the same instant the same way.
	if trendLabelFormat(trendUnitHour) != "YYYY-MM-DD HH24:00" {
		t.Fatalf("hourly SQL format = %q", trendLabelFormat(trendUnitHour))
	}
	if trendLabelFormat(trendUnitDay) != "YYYY-MM-DD" {
		t.Fatalf("daily SQL format = %q", trendLabelFormat(trendUnitDay))
	}
}

func TestParseTrendOffsetClampsNonsense(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"480", 480},
		{"-300", -300},
		{" 480 ", 480},
		{"840", 840},
		{"841", 840},
		{"-841", -840},
		{"99999", 840},
		{"not-a-number", 0},
	} {
		if got := parseTrendOffset(tc.raw); got != tc.want {
			t.Fatalf("parseTrendOffset(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestAlignTrendBucketUsesTheCallersClock is the whole reason the offset exists.
// 15:00 UTC is 23:00 on the same day in UTC+8 but 23:00 on the *next* day at
// UTC+16 — the floor has to move with the caller's midnight, not the server's.
func TestAlignTrendBucketUsesTheCallersClock(t *testing.T) {
	instant := time.Date(2026, 3, 16, 15, 30, 0, 0, time.UTC)

	utc := alignTrendBucket(instant, trendUnitDay, trendOffset(0))
	if got := utc.Format(trendDayLabel); got != "2026-03-16" {
		t.Fatalf("utc day floor = %s, want 2026-03-16", got)
	}

	east := alignTrendBucket(instant, trendUnitDay, trendOffset(480))
	if got := east.Format(trendDayLabel); got != "2026-03-16" {
		t.Fatalf("utc+8 day floor = %s, want 2026-03-16", got)
	}

	// 23:30 UTC is already the 17th in UTC+8, so the floor must be the 17th.
	late := time.Date(2026, 3, 16, 23, 30, 0, 0, time.UTC)
	lateEast := alignTrendBucket(late, trendUnitDay, trendOffset(480))
	if got := lateEast.Format(trendDayLabel); got != "2026-03-17" {
		t.Fatalf("utc+8 day floor for 23:30Z = %s, want 2026-03-17", got)
	}
	lateUTC := alignTrendBucket(late, trendUnitDay, trendOffset(0))
	if got := lateUTC.Format(trendDayLabel); got != "2026-03-16" {
		t.Fatalf("utc day floor for 23:30Z = %s, want 2026-03-16", got)
	}

	// The hourly floor keeps the hour and drops the minutes, in local time.
	hourly := alignTrendBucket(late, trendUnitHour, trendOffset(480))
	if got := hourly.Format(trendHourLabel); got != "2026-03-17 07:00" {
		t.Fatalf("utc+8 hour floor = %s, want 2026-03-17 07:00", got)
	}
}

// TestFillTrendBucketsLeavesNoHoles is the property the chart depends on: a
// quiet period must appear as a zero, not as a missing point that lets the
// neighbouring hours sit side by side as though they were adjacent.
func TestFillTrendBucketsLeavesNoHoles(t *testing.T) {
	from := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 16, 6, 0, 0, 0, time.UTC)

	rows := []model.RequestLogBucket{
		{Bucket: "2026-03-16 01:00", Requests: 4, Tokens: 400, Errors: 1, AvgLatencyMS: 120},
		{Bucket: "2026-03-16 04:00", Requests: 9, Tokens: 900},
	}
	got := fillTrendBuckets(rows, from, to, trendUnitHour, 0)

	if len(got) != 6 {
		t.Fatalf("filled %d buckets, want 6", len(got))
	}
	wantLabels := []string{
		"2026-03-16 00:00", "2026-03-16 01:00", "2026-03-16 02:00",
		"2026-03-16 03:00", "2026-03-16 04:00", "2026-03-16 05:00",
	}
	for i, want := range wantLabels {
		if got[i].Bucket != want {
			t.Fatalf("bucket %d = %q, want %q", i, got[i].Bucket, want)
		}
	}
	// The gap is a real zero.
	if got[2].Requests != 0 || got[2].Tokens != 0 || got[2].Errors != 0 {
		t.Fatalf("the empty bucket is not zero: %#v", got[2])
	}
	// The reported buckets keep their values.
	if got[1].Requests != 4 || got[1].Tokens != 400 || got[1].Errors != 1 || got[1].AvgLatencyMS != 120 {
		t.Fatalf("the reported bucket lost data: %#v", got[1])
	}
	if got[4].Requests != 9 || got[4].Tokens != 900 {
		t.Fatalf("the second reported bucket lost data: %#v", got[4])
	}
}

// TestFillTrendBucketsStartsOnTheCallersBoundary checks that the series begins
// at the local floor of `from` rather than at `from` itself, which is what
// makes the first label line up with the labels the database produced.
func TestFillTrendBucketsStartsOnTheCallersBoundary(t *testing.T) {
	// 16:00Z on the 15th is midnight on the 16th in UTC+8.
	from := time.Date(2026, 3, 15, 16, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 15, 20, 0, 0, 0, time.UTC)

	got := fillTrendBuckets(nil, from, to, trendUnitHour, 480)
	if len(got) != 4 {
		t.Fatalf("filled %d buckets, want 4", len(got))
	}
	for i, want := range []string{
		"2026-03-16 00:00", "2026-03-16 01:00", "2026-03-16 02:00", "2026-03-16 03:00",
	} {
		if got[i].Bucket != want {
			t.Fatalf("bucket %d = %q, want %q", i, got[i].Bucket, want)
		}
	}
}

// TestFillTrendBucketsHandlesAMisalignedStart covers a caller that sends a
// window starting mid-hour: the first label must still be the floor, and the
// series must not begin before the requested range by more than that.
func TestFillTrendBucketsHandlesAMisalignedStart(t *testing.T) {
	from := time.Date(2026, 3, 16, 1, 37, 0, 0, time.UTC)
	to := time.Date(2026, 3, 16, 4, 0, 0, 0, time.UTC)

	got := fillTrendBuckets(nil, from, to, trendUnitHour, 0)
	if len(got) != 3 {
		t.Fatalf("filled %d buckets, want 3", len(got))
	}
	if got[0].Bucket != "2026-03-16 01:00" {
		t.Fatalf("first bucket = %q, want the floor of the start", got[0].Bucket)
	}
}

func TestFillTrendBucketsDailyAcrossDays(t *testing.T) {
	from := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)

	got := fillTrendBuckets(
		[]model.RequestLogBucket{{Bucket: "2026-03-17", Requests: 2}},
		from, to, trendUnitDay, 0,
	)
	if len(got) != 3 {
		t.Fatalf("filled %d days, want 3", len(got))
	}
	for i, want := range []string{"2026-03-16", "2026-03-17", "2026-03-18"} {
		if got[i].Bucket != want {
			t.Fatalf("day %d = %q, want %q", i, got[i].Bucket, want)
		}
	}
	if got[1].Requests != 2 || got[0].Requests != 0 {
		t.Fatalf("daily values are wrong: %#v", got)
	}
}

func TestFillTrendBucketsOnAnEmptyRange(t *testing.T) {
	// A degenerate window produces no points rather than an error: the chart
	// renders its empty state from an empty series.
	at := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	if got := fillTrendBuckets(nil, at, at, trendUnitHour, 0); len(got) != 0 {
		t.Fatalf("a zero-width range produced %d buckets", len(got))
	}
}

// --- request parsing -------------------------------------------------------

// TestRequestLogFilterFromReadsTheNewDimensions covers the three filters the
// table gained. The identity and time fields must reach the storage filter
// unchanged; a typo in a query key would silently return unfiltered rows.
func TestRequestLogFilterFromReadsTheNewDimensions(t *testing.T) {
	from := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)

	req := httptest.NewRequest(http.MethodGet,
		"/admin/request-logs?tenant_id=t-1&api_key_id=k-1&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339),
		nil)
	f, err := requestLogFilterFrom(req, 100)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.TenantID != "t-1" || f.APIKeyID != "k-1" {
		t.Fatalf("identities = %q / %q, want t-1 / k-1", f.TenantID, f.APIKeyID)
	}
	if !f.From.Equal(from) || !f.To.Equal(to) {
		t.Fatalf("range = %s / %s, want %s / %s", f.From, f.To, from, to)
	}
}

func TestRequestLogFilterFromDefaultsFailoverToAny(t *testing.T) {
	// The handler must set the sentinel itself. If it left the zero value, the
	// table would quietly hide every request that failed over.
	f, err := requestLogFilterFrom(httptest.NewRequest(http.MethodGet, "/admin/request-logs", nil), 100)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Failover != -1 {
		t.Fatalf("default failover = %d, want -1 (any)", f.Failover)
	}
}

func TestRequestLogFilterFromRejectsAMalformedRange(t *testing.T) {
	for _, q := range []string{
		"/admin/request-logs?from=yesterday",
		"/admin/request-logs?to=2026-03-16",
		"/admin/request-logs?from=2026-03-16T00:00:00",
	} {
		if _, err := requestLogFilterFrom(httptest.NewRequest(http.MethodGet, q, nil), 100); err == nil {
			t.Fatalf("%s was accepted, want a rejection", q)
		}
	}
}

// --- handler guards --------------------------------------------------------

// trendServer is a Server with no storage. Every test below asserts a guard
// that runs before the database is touched, which is why it is safe.
func trendServer() *Server {
	return &Server{cfg: testConfig()}
}

func trendRequest(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleAdminTrend(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestHandleAdminTrendRejectsWhatItCannotServe(t *testing.T) {
	s := trendServer()
	now := time.Now().UTC()

	for _, tc := range []struct {
		name   string
		target string
		status int
		code   string
	}{
		{
			"unknown bucket",
			"/admin/status/trend?bucket=minute",
			http.StatusBadRequest, "invalid_bucket",
		},
		{
			"malformed from",
			"/admin/status/trend?from=last-tuesday",
			http.StatusBadRequest, "invalid_filter",
		},
		{
			"malformed to",
			"/admin/status/trend?to=2026-03-16",
			http.StatusBadRequest, "invalid_filter",
		},
		{
			"backwards range",
			"/admin/status/trend?from=" + now.Format(time.RFC3339) + "&to=" + now.Add(-time.Hour).Format(time.RFC3339),
			http.StatusBadRequest, "invalid_range",
		},
		{
			"zero-width range",
			"/admin/status/trend?from=" + now.Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339),
			http.StatusBadRequest, "invalid_range",
		},
		{
			"more hourly buckets than the cap",
			"/admin/status/trend?bucket=hour&from=" + now.Add(-721*time.Hour).Format(time.RFC3339) +
				"&to=" + now.Format(time.RFC3339),
			http.StatusBadRequest, "range_too_wide",
		},
		{
			"more daily buckets than the cap",
			"/admin/status/trend?bucket=day&from=" + now.Add(-721*24*time.Hour).Format(time.RFC3339) +
				"&to=" + now.Format(time.RFC3339),
			http.StatusBadRequest, "range_too_wide",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := trendRequest(t, s, tc.target)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestHandleAdminTrendRejectsNonGet(t *testing.T) {
	s := trendServer()
	rec := httptest.NewRecorder()
	s.handleAdminTrend(rec, httptest.NewRequest(http.MethodPost, "/admin/status/trend", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandleRequestLogsSummaryRejectsAMalformedRange(t *testing.T) {
	s := trendServer()
	rec := httptest.NewRecorder()
	s.handleRequestLogsSummary(rec, httptest.NewRequest(http.MethodGet, "/admin/request-logs/summary?from=soon", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := errorCode(t, rec); got != "invalid_filter" {
		t.Fatalf("code = %q, want invalid_filter", got)
	}
}

// TestRevokeAPIKeyRejectsANonUUID is the guard that keeps a malformed path
// segment out of the database, where the uuid cast would fail the statement and
// turn a client error into a 500.
func TestRevokeAPIKeyRejectsANonUUID(t *testing.T) {
	s := trendServer()
	rec := httptest.NewRecorder()
	s.revokeAPIKey(rec, httptest.NewRequest(http.MethodDelete, "/admin/api-keys/not-a-uuid", nil), "not-a-uuid", "test-req")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := errorCode(t, rec); got != "api_key_not_found" {
		t.Fatalf("code = %q, want api_key_not_found", got)
	}
}

// TestHandleAPIKeyByIDAdvertisesItsMethods checks the Allow header, which is
// what tells a client that DELETE now exists.
func TestHandleAPIKeyByIDAdvertisesItsMethods(t *testing.T) {
	s := trendServer()
	rec := httptest.NewRecorder()
	s.handleAPIKeyByID(rec, httptest.NewRequest(http.MethodPatch, "/admin/api-keys/abc", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "PUT, DELETE" {
		t.Fatalf("Allow = %q, want \"PUT, DELETE\"", allow)
	}
}

func TestHandleAPIKeyByIDWithoutAnIDIsNotFound(t *testing.T) {
	s := trendServer()
	rec := httptest.NewRecorder()
	s.handleAPIKeyByID(rec, httptest.NewRequest(http.MethodPut, "/admin/api-keys/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
