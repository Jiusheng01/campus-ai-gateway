package storage

import (
	"context"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestRuntimeSummariesFilterByModelAndProviderWithoutTenantIdentity(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	for _, stmt := range []string{`DELETE FROM gateway_request_attempt`, `DELETE FROM gateway_request_log`} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	provider := mustProvider(t, pg, "runtime-provider")
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "runtime-model", Status: 1, ProviderID: provider.ID, UpstreamModel: "runtime-up",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	for i, status := range []int{200, 502} {
		if err := pg.InsertRequestLog(ctx, model.RequestLog{
			RequestID: "runtime-req-" + string(rune('a'+i)), RequestedModel: "runtime-model",
			ActualModel: "runtime-up", Provider: provider.Name, UpstreamModel: "runtime-up",
			RoutePriority: 1, Attempts: 1, Failover: i > 0, TotalTokens: 10,
			LatencyMS: 12, StatusCode: status,
		}); err != nil {
			t.Fatalf("InsertRequestLog: %v", err)
		}
	}
	if err := pg.InsertRequestAttempts(ctx, "runtime-req-b", []model.RequestAttempt{
		{AttemptNo: 1, Provider: provider.Name, UpstreamModel: "runtime-up", Outcome: model.AttemptFailed, StatusCode: 502, LatencyMS: 5, ErrorKind: "upstream_error"},
		{AttemptNo: 2, Provider: "other-provider", UpstreamModel: "other-up", Outcome: model.AttemptSkipped},
	}); err != nil {
		t.Fatalf("InsertRequestAttempts: %v", err)
	}

	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	modelSummary, err := pg.ModelRuntimeSummary(ctx, created.ID, from, to)
	if err != nil {
		t.Fatalf("ModelRuntimeSummary: %v", err)
	}
	if modelSummary.Alias != "runtime-model" || modelSummary.Totals.Requests != 2 || modelSummary.Totals.Errors != 1 {
		t.Fatalf("model summary = %+v", modelSummary)
	}
	if len(modelSummary.Recent) != 2 {
		t.Fatalf("recent = %+v", modelSummary.Recent)
	}

	providerSummary, err := pg.ProviderRuntimeSummary(ctx, provider.ID, from, to)
	if err != nil {
		t.Fatalf("ProviderRuntimeSummary: %v", err)
	}
	if providerSummary.Provider != provider.Name || providerSummary.Totals.Requests != 2 {
		t.Fatalf("provider summary = %+v", providerSummary)
	}
	if providerSummary.Attempts.Failed != 1 {
		t.Fatalf("attempt outcomes = %+v", providerSummary.Attempts)
	}
}
