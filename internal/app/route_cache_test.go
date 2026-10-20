package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func routeTableWith(name string) routeTable {
	return routeTable{models: map[string][]directRoute{
		name: {{Provider: name, UpstreamModel: "up-" + name}},
	}}
}

// A rebuild that started before a write must not publish its stale result over
// the new configuration.
func TestRouteCacheDiscardsBuildThatOverlappedInvalidation(t *testing.T) {
	s := &Server{}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	s.routeBuildFn = func(context.Context) (routeTable, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			close(started)
			<-release
			return routeTableWith("old"), nil
		}
		return routeTableWith("new"), nil
	}

	type result struct {
		table routeTable
		err   error
	}
	done := make(chan result, 1)
	go func() {
		table, err := s.routeTableCached(context.Background())
		done <- result{table, err}
	}()
	<-started
	s.invalidateRouteCache()
	close(release)

	got := <-done
	if got.err != nil {
		t.Fatalf("routeTableCached: %v", got.err)
	}
	if _, ok := got.table.models["new"]; !ok {
		t.Fatalf("the stale build won: %+v", got.table.models)
	}
	if _, ok := got.table.models["old"]; ok {
		t.Fatalf("the discarded build was served: %+v", got.table.models)
	}
	if calls < 2 {
		t.Fatalf("build calls = %d, want at least 2", calls)
	}
	state := s.routeCacheState()
	if state.Generation != 1 || state.BuiltAt == nil {
		t.Fatalf("cache state = %+v", state)
	}
}

func TestRouteCacheKeepsPreviousTableAndMarksStaleOnBuildFailure(t *testing.T) {
	s := &Server{
		routeCache: routeTableWith("previous"),
		routeAt:    time.Time{},
	}
	s.routeBuildFn = func(context.Context) (routeTable, error) {
		return routeTable{}, errors.New("database down")
	}
	got, err := s.routeTableCached(context.Background())
	if err != nil {
		t.Fatalf("routeTableCached should serve the previous table: %v", err)
	}
	if _, ok := got.models["previous"]; !ok {
		t.Fatalf("previous table not served: %+v", got.models)
	}
	state := s.routeCacheState()
	if !state.Stale || state.LastErrorKind == "" {
		t.Fatalf("cache state = %+v, want stale with a redacted error kind", state)
	}
}
