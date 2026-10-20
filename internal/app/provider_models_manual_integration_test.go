package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// An operator can supplement the list by hand — a cold model the vendor does
// not advertise, or a channel that has no preset at all. The row says
// source=manual so the console can tell it apart from what the gateway seeded
// or discovered. Adding a name that already exists is not an error and does not
// relabel it.
func TestManualProviderModelCanBeAddedAndDeleted(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models",
		bytes.NewReader([]byte(`{"upstream_model":"internal-embed-v2"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("manual add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var added model.ProviderModel
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode added model: %v", err)
	}
	if added.Source != model.ProviderModelSourceManual || added.UpstreamModel != "internal-embed-v2" {
		t.Fatalf("added model = %+v, want a manual internal-embed-v2", added)
	}

	// Adding the same name again is idempotent: 200, same row, still one entry.
	rec = httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models",
		bytes.NewReader([]byte(`{"upstream_model":"internal-embed-v2"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("repeated manual add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if models := listChannelModelsThroughAPI(t, s, created.ID); len(models) != 1 || models[0].ID != added.ID {
		t.Fatalf("a repeated add must not create a second row: %+v", models)
	}

	rec = httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodDelete,
		"/admin/providers/"+created.ID+"/models/"+added.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if models := listChannelModelsThroughAPI(t, s, created.ID); len(models) != 0 {
		t.Fatalf("the deleted model is still listed: %+v", models)
	}

	rec = httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodDelete,
		"/admin/providers/"+created.ID+"/models/"+added.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleting again status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestManualProviderModelRejectsAnEmptyName(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models",
		bytes.NewReader([]byte(`{"upstream_model":"   "}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if models := listChannelModelsThroughAPI(t, s, created.ID); len(models) != 0 {
		t.Fatalf("an empty name must not reach the list: %+v", models)
	}
}
