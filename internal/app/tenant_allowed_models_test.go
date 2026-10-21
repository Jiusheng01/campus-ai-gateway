package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The Tenant allow-list has the same three shapes as a key's, but a different
// omitted-field default: a Tenant that never mentioned the field allows every
// model. The empty list is still a decision. These are the exact inputs the
// admin form sends, so they are asserted at the validator the two tenant
// endpoints both call.
func TestValidateTenantNormalisesAllowedModels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "omitted means the wildcard default", in: nil, want: []string{model.AllModelsWildcard}},
		{name: "the wildcard stands alone", in: []string{model.AllModelsWildcard}, want: []string{model.AllModelsWildcard}},
		{name: "an explicit list is sorted", in: []string{"campus-code", "campus-gpt"}, want: []string{"campus-code", "campus-gpt"}},
		{name: "duplicates collapse", in: []string{"campus-gpt", "campus-gpt"}, want: []string{"campus-gpt"}},
		{name: "an empty list denies everything", in: []string{}, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := model.Tenant{Code: "dept", Name: "Dept", AllowedModels: tc.in}
			if reason := validateTenant(&v); reason != "" {
				t.Fatalf("unexpected rejection: %q", reason)
			}
			if !reflect.DeepEqual(v.AllowedModels, tc.want) {
				t.Fatalf("allowed_models = %#v, want %#v", v.AllowedModels, tc.want)
			}
		})
	}
}

func TestValidateTenantRejectsAmbiguousAllowedModels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{name: "wildcard mixed with names", in: []string{model.AllModelsWildcard, "campus-gpt"}, want: "wildcard"},
		{name: "blank entry", in: []string{"campus-gpt", "  "}, want: "empty entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := model.Tenant{Code: "dept", Name: "Dept", AllowedModels: tc.in}
			reason := validateTenant(&v)
			if !strings.Contains(reason, tc.want) {
				t.Fatalf("reason = %q, want it to mention %q", reason, tc.want)
			}
		})
	}
}

// Validation runs before storage, so the two tenant endpoints can be shown to
// answer 400 with the reason without PostgreSQL. An ambiguous scope must be an
// operator-visible error, not a value quietly reinterpreted on write.
func TestTenantAllowedModelsWritePathRejectsAmbiguousLists(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "wildcard mixed with a named model",
			body: `{"code":"dept","name":"Dept","allowed_models":["*","campus-gpt"]}`,
			want: "wildcard",
		},
		{
			name: "a blank entry",
			body: `{"code":"dept","name":"Dept","allowed_models":["campus-gpt","  "]}`,
			want: "empty entry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(tc.body))

			s.handleTenants(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			var out struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if out.Error.Code != "invalid_tenant" {
				t.Fatalf("code = %q, want invalid_tenant", out.Error.Code)
			}
			if !strings.Contains(out.Error.Message, tc.want) {
				t.Fatalf("the rejection must say why (%q missing): %q", tc.want, out.Error.Message)
			}
		})
	}
}

// The three shapes must survive both writes and the read the console uses to
// edit a Tenant. The API seam is the contract here: after POST and after PUT,
// the returned document and the list row are what the form will render.
func TestTenantAllowedModelsRoundTripThroughTheAdminAPI(t *testing.T) {
	pg := usagePostgres(t)
	s := &Server{pg: pg}

	shapes := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "wildcard", in: []string{model.AllModelsWildcard}, want: []string{model.AllModelsWildcard}},
		{name: "explicit", in: []string{"campus-gpt", "campus-code"}, want: []string{"campus-code", "campus-gpt"}},
		{name: "none", in: []string{}, want: []string{}},
	}

	post := func(t *testing.T, body map[string]any) model.Tenant {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rec := httptest.NewRecorder()
		s.handleTenants(rec, httptest.NewRequest(http.MethodPost, "/admin/tenants", bytes.NewReader(raw)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
		}
		var out model.Tenant
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return out
	}

	put := func(t *testing.T, id string, body map[string]any) model.Tenant {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rec := httptest.NewRecorder()
		s.handleTenantByID(rec, httptest.NewRequest(http.MethodPut, "/admin/tenants/"+id, bytes.NewReader(raw)))
		if rec.Code != http.StatusOK {
			t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
		}
		var out model.Tenant
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		return out
	}

	for i, tc := range shapes {
		t.Run(tc.name, func(t *testing.T) {
			created := post(t, map[string]any{
				"code":           "allow-" + tc.name,
				"name":           "Allow " + tc.name,
				"allowed_models": tc.in,
			})
			if !reflect.DeepEqual(created.AllowedModels, tc.want) {
				t.Fatalf("created allowed_models = %#v, want %#v", created.AllowedModels, tc.want)
			}

			// Edit into the next shape, so every shape is exercised as both a
			// create and an edit rather than only one side of the round trip.
			next := shapes[(i+1)%len(shapes)]
			updated := put(t, created.ID, map[string]any{
				"code":           created.Code,
				"name":           created.Name,
				"status":         created.Status,
				"allowed_models": next.in,
			})
			if !reflect.DeepEqual(updated.AllowedModels, next.want) {
				t.Fatalf("updated allowed_models = %#v, want %#v", updated.AllowedModels, next.want)
			}

			rec := httptest.NewRecorder()
			s.handleTenants(rec, httptest.NewRequest(http.MethodGet, "/admin/tenants", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
			}
			var listed struct {
				Data []model.Tenant `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
				t.Fatalf("decode list: %v", err)
			}
			var found bool
			for _, v := range listed.Data {
				if v.ID != created.ID {
					continue
				}
				found = true
				if !reflect.DeepEqual(v.AllowedModels, next.want) {
					t.Fatalf("listed allowed_models = %#v, want %#v", v.AllowedModels, next.want)
				}
			}
			if !found {
				t.Fatal("the edited tenant is missing from ListTenants")
			}
		})
	}

	// An omitted field is the pre-existing Tenant's meaning: unrestricted.
	t.Run("omitted defaults to wildcard", func(t *testing.T) {
		created := post(t, map[string]any{"code": "allow-default", "name": "Allow Default"})
		if !reflect.DeepEqual(created.AllowedModels, []string{model.AllModelsWildcard}) {
			t.Fatalf("omitted allowed_models = %#v, want [\"*\"]", created.AllowedModels)
		}
	})
}

// An edit that omits allowed_models must leave the stored allowlist exactly as
// it was. Treating absence as the create-time wildcard default would turn a
// rename into a silent widening, and treating it as [] would turn the same
// rename into a silent deny-all; both are the bug this test pins down.
func TestTenantUpdateDistinguishesOmittedAllowedModelsFromEmptyAndWildcard(t *testing.T) {
	pg := usagePostgres(t)
	s := &Server{pg: pg}

	create := func(t *testing.T, code string, allowed []string) model.Tenant {
		t.Helper()
		rec := httptest.NewRecorder()
		body := map[string]any{"code": code, "name": code, "allowed_models": allowed}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s.handleTenants(rec, httptest.NewRequest(http.MethodPost, "/admin/tenants", bytes.NewReader(raw)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
		}
		var out model.Tenant
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return out
	}

	put := func(t *testing.T, id string, body map[string]any) model.Tenant {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rec := httptest.NewRecorder()
		s.handleTenantByID(rec, httptest.NewRequest(http.MethodPut, "/admin/tenants/"+id, bytes.NewReader(raw)))
		if rec.Code != http.StatusOK {
			t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
		}
		var out model.Tenant
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		return out
	}

	t.Run("omitted keeps an explicit list", func(t *testing.T) {
		created := create(t, "put-keep-explicit", []string{"campus-gpt", "campus-code"})
		updated := put(t, created.ID, map[string]any{
			"code": created.Code, "name": "Renamed", "status": created.Status,
			// allowed_models deliberately absent.
		})
		if !reflect.DeepEqual(updated.AllowedModels, []string{"campus-code", "campus-gpt"}) {
			t.Fatalf("omitted allowed_models = %#v, want the stored explicit list", updated.AllowedModels)
		}
		if updated.Name != "Renamed" {
			t.Fatalf("the other fields must still be written, name = %q", updated.Name)
		}
	})

	t.Run("explicit empty list clears the scope", func(t *testing.T) {
		created := create(t, "put-clear", []string{"campus-gpt"})
		updated := put(t, created.ID, map[string]any{
			"code": created.Code, "name": created.Name, "status": created.Status,
			"allowed_models": []string{},
		})
		if updated.AllowedModels == nil || len(updated.AllowedModels) != 0 {
			t.Fatalf("allowed_models = %#v, want an explicit empty list", updated.AllowedModels)
		}
	})

	t.Run("explicit wildcard widens", func(t *testing.T) {
		created := create(t, "put-widen", []string{"campus-gpt"})
		updated := put(t, created.ID, map[string]any{
			"code": created.Code, "name": created.Name, "status": created.Status,
			"allowed_models": []string{model.AllModelsWildcard},
		})
		if !reflect.DeepEqual(updated.AllowedModels, []string{model.AllModelsWildcard}) {
			t.Fatalf("allowed_models = %#v, want the wildcard", updated.AllowedModels)
		}
	})

	t.Run("omitted on a deny-all tenant keeps the empty list", func(t *testing.T) {
		created := create(t, "put-keep-empty", []string{})
		updated := put(t, created.ID, map[string]any{
			"code": created.Code, "name": "Still Empty", "status": created.Status,
		})
		if updated.AllowedModels == nil || len(updated.AllowedModels) != 0 {
			t.Fatalf("allowed_models = %#v, want the stored empty list", updated.AllowedModels)
		}
	})
}
