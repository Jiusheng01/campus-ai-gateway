package storage

import (
	"context"
	"fmt"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// OnboardingCounts reads the deployment state the setup checklist describes.
//
// One statement, because the numbers are unrelated to each other and five
// round trips would only make the first screen slower. Every count is scoped to
// the whole deployment: this endpoint is only granted to global administrators,
// and a scoped one lands on its subtree pages instead of the dashboard.
func (p *Postgres) OnboardingCounts(ctx context.Context) (model.OnboardingCounts, error) {
	var v model.OnboardingCounts
	err := p.Pool.QueryRow(ctx, `
SELECT
	(SELECT COUNT(*) FROM gateway_provider),
	(SELECT COUNT(*) FROM gateway_provider WHERE status = 1),
	(SELECT COUNT(*) FROM gateway_model),
	(SELECT COUNT(*) FROM gateway_model WHERE status = 1),
	(SELECT COUNT(DISTINCT r.model_id)
	   FROM gateway_model_route r
	   JOIN gateway_model m ON m.id = r.model_id
	  WHERE r.status = 1 AND m.status = 1),
	(SELECT COUNT(*) FROM gateway_tenant),
	(SELECT COUNT(*) FROM gateway_api_key WHERE status = 1),
	(SELECT COUNT(*) FROM gateway_request_log),
	COALESCE((
		SELECT m.alias
		  FROM gateway_model m
		  JOIN gateway_model_route r ON r.model_id = m.id
		 WHERE m.status = 1 AND r.status = 1
		 GROUP BY m.alias
		 ORDER BY m.alias
		 LIMIT 1
	), '')
`).Scan(
		&v.Providers, &v.EnabledProviders,
		&v.Models, &v.EnabledModels, &v.RoutableModels,
		&v.Tenants, &v.EnabledAPIKeys, &v.Requests, &v.SampleModel,
	)
	if err != nil {
		return model.OnboardingCounts{}, fmt.Errorf("load onboarding counts: %w", err)
	}
	return v, nil
}
