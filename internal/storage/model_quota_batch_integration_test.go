package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// TestEffectiveModelQuotasForTenants checks the batch resolver the admin list
// uses. It must answer exactly the subjects it was asked for, reuse the
// tightest-positive walk, and skip ids that no longer exist instead of failing
// the whole page.
func TestEffectiveModelQuotasForTenants(t *testing.T) {
	pg := testPostgres(t)
	clearTenantsAndKeys(t, pg)

	ctx := context.Background()
	const alias = "effective-batch-test"
	reset := func() {
		_, _ = pg.Pool.Exec(ctx, `DELETE FROM gateway_model_quota WHERE model_alias = $1`, alias)
		_, _ = pg.Pool.Exec(ctx, `DELETE FROM gateway_model WHERE alias = $1`, alias)
	}
	reset()
	t.Cleanup(reset)

	if _, err := pg.CreateModel(ctx, model.ModelConfig{Alias: alias, DisplayName: "Batch quota test", Status: 1}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	school, err := pg.CreateTenant(ctx, model.Tenant{Code: "eq-school", Name: "学校", Status: 1})
	if err != nil {
		t.Fatalf("create school: %v", err)
	}
	dept, err := pg.CreateTenant(ctx, model.Tenant{Code: "eq-dept", Name: "计算机学院", Status: 1, ParentID: &school.ID})
	if err != nil {
		t.Fatalf("create department: %v", err)
	}

	setQuota := func(tenantID *string, daily, monthly *int64) {
		t.Helper()
		if _, err := pg.UpsertModelQuota(ctx, model.ModelQuotaAllocation{
			TenantID:          tenantID,
			ModelAlias:        alias,
			DailyTokenLimit:   daily,
			MonthlyTokenLimit: monthly,
		}); err != nil {
			t.Fatalf("set quota for %v: %v", tenantID, err)
		}
	}
	setQuota(nil, i64(1000), i64(20000)) // shared pool
	setQuota(&school.ID, i64(500), nil)  // root tightens the daily cap
	setQuota(&dept.ID, nil, i64(8000))   // department tightens the monthly cap

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := pg.EffectiveModelQuotasForTenants(ctx, []string{dept.ID, school.ID, dept.ID, missing})
	if err != nil {
		t.Fatalf("resolve batches: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("resolved %d subjects, want 2 (duplicate and missing ids collapse)", len(got))
	}

	deptQuotas := got[dept.ID]
	if len(deptQuotas) != 1 {
		t.Fatalf("department has %d effective quotas, want 1", len(deptQuotas))
	}
	dq := deptQuotas[0]
	if dq.DailyTokenLimit != 500 || dq.MonthlyTokenLimit != 8000 {
		t.Fatalf("department quota = daily %d / monthly %d, want 500 / 8000", dq.DailyTokenLimit, dq.MonthlyTokenLimit)
	}
	if dq.SetBy != "学校" {
		t.Fatalf("department SetBy = %q, want the root that set the daily cap", dq.SetBy)
	}
	if dq.Own {
		t.Fatal("department quota reported as its own although the daily cap came from the root")
	}

	schoolQuotas := got[school.ID]
	if len(schoolQuotas) != 1 {
		t.Fatalf("school has %d effective quotas, want 1", len(schoolQuotas))
	}
	sq := schoolQuotas[0]
	if sq.DailyTokenLimit != 500 || sq.MonthlyTokenLimit != 20000 {
		t.Fatalf("school quota = daily %d / monthly %d, want 500 / 20000", sq.DailyTokenLimit, sq.MonthlyTokenLimit)
	}
	if !sq.Own {
		t.Fatal("school quota should report the root's own daily cap")
	}

	if _, ok := got[missing]; ok {
		t.Fatal("a missing tenant id produced an entry")
	}
}
