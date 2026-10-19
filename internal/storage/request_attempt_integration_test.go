package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The attempt chain of one request must round-trip in order, and an empty chain
// must write nothing rather than a row with no attempt number.
func TestRequestAttemptsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	const reqID = "req-attempt-roundtrip"
	const clean = `DELETE FROM gateway_request_attempt WHERE request_id=$1`
	_, _ = pg.Pool.Exec(ctx, clean, reqID)
	t.Cleanup(func() { _, _ = pg.Pool.Exec(context.Background(), clean, reqID) })

	in := []model.RequestAttempt{
		{AttemptNo: 1, Provider: "a", UpstreamModel: "a-model", Outcome: model.AttemptFailed, StatusCode: 500, LatencyMS: 12, ErrorKind: ""},
		{AttemptNo: 2, Provider: "b", UpstreamModel: "b-model", Outcome: model.AttemptServed, StatusCode: 200, LatencyMS: 34},
	}
	if err := pg.InsertRequestAttempts(ctx, reqID, in); err != nil {
		t.Fatalf("InsertRequestAttempts: %v", err)
	}

	got, err := pg.RequestAttempts(ctx, reqID)
	if err != nil {
		t.Fatalf("RequestAttempts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attempts, want 2: %+v", len(got), got)
	}
	if got[0].AttemptNo != 1 || got[0].Provider != "a" || got[0].Outcome != model.AttemptFailed || got[0].StatusCode != 500 {
		t.Fatalf("first attempt = %+v", got[0])
	}
	if got[1].AttemptNo != 2 || got[1].Provider != "b" || got[1].Outcome != model.AttemptServed || got[1].UpstreamModel != "b-model" {
		t.Fatalf("second attempt = %+v", got[1])
	}

	// An empty chain is a no-op, not an error and not a stray row.
	if err := pg.InsertRequestAttempts(ctx, "req-attempt-empty", nil); err != nil {
		t.Fatalf("empty insert: %v", err)
	}
	if rows, err := pg.RequestAttempts(ctx, "req-attempt-empty"); err != nil || len(rows) != 0 {
		t.Fatalf("empty chain wrote rows: %v (err %v)", rows, err)
	}
}
