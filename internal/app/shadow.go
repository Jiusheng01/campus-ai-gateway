package app

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// shadowChain returns the ordered Routing Attempts the direct plane would make
// for a model, recorded as shadow rows (leader first, then the fallback order).
//
// It advances the same smooth-weighted-round-robin rotation a live request
// would, so the recorded sequence is the one the cutover would produce —
// recording without advancing would sample the same leader every time and prove
// nothing. It dials nothing.
//
// The circuit breaker is deliberately not consulted: in shadow mode the direct
// plane has sent nothing, so it has no failures to break on, and letting the
// check mark a half-open probe in flight (which nothing would ever resolve)
// would corrupt the state the cutover depends on.
//
// nil is returned when no enabled route exists for the model — the direct plane
// would have answered 404, so there is no decision to record.
func (s *Server) shadowChain(ctx context.Context, alias string) []model.RequestAttempt {
	if s.selector == nil {
		return nil
	}
	table, err := s.routeTableCached(ctx)
	if err != nil {
		return nil
	}
	configured := table.models[alias]
	if len(configured) == 0 {
		return nil
	}
	ordered := s.orderRoutes(alias, configured)
	out := make([]model.RequestAttempt, 0, len(ordered))
	for i, route := range ordered {
		out = append(out, model.RequestAttempt{
			AttemptNo:     i + 1,
			Provider:      route.Provider,
			UpstreamModel: route.UpstreamModel,
			Outcome:       model.AttemptShadow,
		})
	}
	return out
}
