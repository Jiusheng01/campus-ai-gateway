package storage

import (
	"context"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The shadow report must surface a request where the direct plane's decision
// named a different channel than ThinkWatch served through.
func TestShadowReportFindsDisagreements(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)

	const reqID = "req-shadow-report-1"
	const cleanAttempts = `DELETE FROM gateway_request_attempt WHERE request_id=$1`
	const cleanLog = `DELETE FROM gateway_request_log WHERE request_id=$1`
	_, _ = pg.Pool.Exec(ctx, cleanAttempts, reqID)
	_, _ = pg.Pool.Exec(ctx, cleanLog, reqID)
	t.Cleanup(func() {
		_, _ = pg.Pool.Exec(context.Background(), cleanAttempts, reqID)
		_, _ = pg.Pool.Exec(context.Background(), cleanLog, reqID)
	})

	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO gateway_request_log(request_id, provider) VALUES ($1, 'thinkwatch-channel')`, reqID); err != nil {
		t.Fatal(err)
	}
	if err := pg.InsertRequestAttempts(ctx, reqID, []model.RequestAttempt{
		{AttemptNo: 1, Provider: "direct-channel", UpstreamModel: "m", Outcome: model.AttemptShadow},
	}); err != nil {
		t.Fatal(err)
	}

	sampled, mismatched, rows, err := pg.ShadowReport(ctx, time.Now().UTC().Add(-time.Hour), 50)
	if err != nil {
		t.Fatalf("ShadowReport: %v", err)
	}
	if sampled < 1 || mismatched < 1 {
		t.Fatalf("sampled = %d, mismatched = %d, want at least 1 each", sampled, mismatched)
	}
	for _, r := range rows {
		if r.RequestID != reqID {
			continue
		}
		if r.DirectProvider != "direct-channel" || r.ThinkWatchProvider != "thinkwatch-channel" {
			t.Fatalf("row = %+v, want direct-channel vs thinkwatch-channel", r)
		}
		return
	}
	t.Fatalf("the disagreement for %s is missing from %+v", reqID, rows)
}
