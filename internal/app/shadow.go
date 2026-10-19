package app

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// shadowDecision reports the channel the direct plane would pick first for a
// model, so it can be compared with the channel ThinkWatch actually chose for
// the same request.
//
// It advances the same smooth-weighted-round-robin rotation a live request
// would, so the recorded distribution is the one the cutover would produce —
// recording without advancing would sample the same leader every time and prove
// nothing. It dials nothing.
//
// The circuit breaker is deliberately not consulted: in shadow mode the direct
// plane has sent nothing, so it has no failures to break on, and letting the
// check mark a half-open probe in flight (which nothing would ever resolve)
// would corrupt the state the cutover depends on.
//
// ok is false when no enabled route exists for the model — the direct plane
// would have answered 404, so there is no decision to record.
func (s *Server) shadowDecision(ctx context.Context, alias string) (model.RequestAttempt, bool) {
	if s.selector == nil {
		// No rotation configured: only reachable if the direct-plane state was
		// never built, which shadow mode always builds.
		return model.RequestAttempt{}, false
	}
	table, err := s.routeTableCached(ctx)
	if err != nil {
		return model.RequestAttempt{}, false
	}
	configured := table.models[alias]
	if len(configured) == 0 {
		return model.RequestAttempt{}, false
	}
	leader := s.orderRoutes(alias, configured)[0]
	return model.RequestAttempt{
		AttemptNo:     1,
		Provider:      leader.Provider,
		UpstreamModel: leader.UpstreamModel,
		Outcome:       model.AttemptShadow,
	}, true
}
