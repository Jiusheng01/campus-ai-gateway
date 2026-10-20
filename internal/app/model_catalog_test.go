package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The open endpoint validates its input before it touches storage, so these
// cases run without a database. They pin the contract the console relies on:
// the body names one Provider Model row, and anything else is a 400 rather
// than a lookup.
func TestOpenCatalogModelRejectsAnInvalidRequest(t *testing.T) {
	s := &Server{}
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"not json", `{`},
		{"missing id", `{}`},
		{"blank id", `{"provider_model_id":"  "}`},
		{"not a uuid", `{"provider_model_id":"deepseek-chat"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.handleOpenCatalogModel(rec, httptest.NewRequest(http.MethodPost,
				"/admin/model-catalog/open", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := httptest.NewRecorder()
	s.handleOpenCatalogModel(rec, httptest.NewRequest(http.MethodGet, "/admin/model-catalog/open", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestModelCatalogIsReadOnly(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleModelCatalog(rec, httptest.NewRequest(http.MethodPost, "/admin/model-catalog", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}
