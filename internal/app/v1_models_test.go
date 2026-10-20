package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestModelsFromTableIntersectsAllowListAndStaticRoutes(t *testing.T) {
	// routeTable only contains aliases with at least one enabled line; a
	// disabled model is absent, not present with an empty slice.
	table := routeTable{models: map[string][]directRoute{
		"allowed": {{Provider: "p", UpstreamModel: "u"}},
		"denied":  {{Provider: "p", UpstreamModel: "u"}},
	}}
	read := func(allowed map[string]struct{}, all bool) []string {
		rec := httptest.NewRecorder()
		writeModelsFromTable(rec, table, allowed, all)
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("response: %v", err)
		}
		names := make([]string, 0, len(out.Data))
		for _, d := range out.Data {
			names = append(names, d.ID)
		}
		return names
	}

	if got := read(map[string]struct{}{"allowed": {}, "denied": {}}, false); len(got) != 2 || got[0] != "allowed" || got[1] != "denied" {
		t.Fatalf("restricted = %v", got)
	}
	if got := read(map[string]struct{}{"allowed": {}}, false); len(got) != 1 || got[0] != "allowed" {
		t.Fatalf("single allow = %v", got)
	}
	if got := read(map[string]struct{}{}, false); len(got) != 0 {
		t.Fatalf("empty allow = %v", got)
	}
	if got := read(nil, true); len(got) != 2 {
		t.Fatalf("wildcard = %v", got)
	}
}
