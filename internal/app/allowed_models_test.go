package app

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The allow-list is canonicalised inside validateAPIKey, and the only way an
// operator reaches it is the key write endpoint. Rejecting an ambiguous or
// blank list is therefore an HTTP answer with a code the console can show, not
// just a pure-function property. These inputs are refused before any database
// work, so the write path can be exercised without PostgreSQL.
func TestAllowedModelsWritePathRejectsAmbiguousLists(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "wildcard mixed with a named model",
			body:    `{"name":"k","tenant_id":"t","allowed_models":["*","demo-model"]}`,
			wantMsg: "wildcard",
		},
		{
			name:    "a blank entry is a mistake, not an omission",
			body:    `{"name":"k","tenant_id":"t","allowed_models":["demo-model","  "]}`,
			wantMsg: "empty entry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No database is needed: validation runs before the storage call.
			s := &Server{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/api-keys", strings.NewReader(tc.body))

			s.handleAPIKeys(rec, req)

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
			if out.Error.Code != "invalid_api_key_config" {
				t.Fatalf("code = %q, want invalid_api_key_config", out.Error.Code)
			}
			if !strings.Contains(out.Error.Message, tc.wantMsg) {
				t.Fatalf("the rejection must say why (%q missing): %q", tc.wantMsg, out.Error.Message)
			}
		})
	}
}

// A key with nothing ticked is stored as an explicit empty list, not null.
// null is what the JSON marshaller produces for a nil slice, and null in the
// column is exactly the "unset" shape that the two readings grew out of: an
// empty list has to survive the round trip as a decision.
func TestAllowedModelsPersistAsAnExplicitEmptyList(t *testing.T) {
	var missing []string
	if reason := normalizeAllowedModels(&missing); reason != "" {
		t.Fatalf("unexpected rejection: %q", reason)
	}
	raw, err := json.Marshal(missing)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != "[]" {
		t.Fatalf("a key with no models must persist as [], got %s", raw)
	}
}

// A restricted key must advertise exactly the models it may call. Answering
// from the routing table, or expanding the wildcard, would advertise models the
// key has just been refused — and a key allowed nothing must advertise nothing.
func TestWriteAllowedModelsListsExactlyTheAllowedSet(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAllowedModels(rec, map[string]struct{}{"demo-vision": {}, "demo-model": {}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if out.Object != "list" {
		t.Fatalf("object = %q, want list", out.Object)
	}
	if len(out.Data) != 2 || out.Data[0].ID != "demo-model" || out.Data[1].ID != "demo-vision" {
		t.Fatalf("models = %+v, want demo-model then demo-vision", out.Data)
	}

	rec = httptest.NewRecorder()
	writeAllowedModels(rec, map[string]struct{}{})
	var empty struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(empty.Data) != 0 {
		t.Fatalf("a key allowed nothing must advertise nothing, got %+v", empty.Data)
	}
}

// The backfill can only be run against PostgreSQL, which the test environment
// does not provide, but its two structural promises are readable in the SQL and
// are the parts that keep the upgrade from changing live access:
//
//   - only rows the old reading treated as unrestricted are rewritten - an empty
//     list, and the JSON null a writer produced for an omitted field - so a key
//     that was already wildcarded or already restricted keeps its scope;
//   - the UPDATE sits behind an existence guard, so a database missing the
//     column is reported by the schema contract (table, column, expected shape)
//     instead of failing first with "column does not exist".
func TestAllowedModelsMigrationRewritesOnlyTheUnrestrictedShapes(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0011_allowed_models_explicit.sql"))
	if err != nil {
		t.Fatalf("the backfill migration must exist: %v", err)
	}
	sql := string(body)

	wantSet := `SET allowed_models = '["` + model.AllModelsWildcard + `"]'::jsonb`
	if !strings.Contains(sql, wantSet) {
		t.Fatalf("the migration must rewrite the empty list to the wildcard (%q missing):\n%s", wantSet, sql)
	}
	if !strings.Contains(sql, `WHERE allowed_models = '[]'::jsonb`) {
		t.Fatalf("the migration must only touch rows whose list is empty or null:\n%s", sql)
	}
	// JSON null is what the marshaller wrote when the field was omitted, and the
	// old reading counted it as unrestricted too. Dropping it from the predicate
	// would strand those keys on a reading that denies every model.
	if !strings.Contains(sql, `OR allowed_models = 'null'::jsonb`) {
		t.Fatalf("the migration must also rewrite the rows holding JSON null:\n%s", sql)
	}

	// Search for the statement, not the word: the header comment also contains
	// "UPDATE" while explaining why the guard exists.
	block := strings.Index(sql, "DO $$")
	guard := strings.Index(sql, "IF EXISTS")
	update := strings.Index(sql, "UPDATE gateway_api_key")
	if block < 0 || guard < 0 || update < 0 || block > guard || guard > update {
		t.Fatalf("the UPDATE must sit inside a DO block behind an existence guard:\n%s", sql)
	}
	if !strings.Contains(sql, "information_schema.columns") ||
		!strings.Contains(sql, "column_name = 'allowed_models'") ||
		!strings.Contains(sql, "table_name = 'gateway_api_key'") {
		t.Fatalf("the guard must name the table and column the contract check reports:\n%s", sql)
	}
}

// The inverted default this release removes can only return through a second
// call site that works out for itself whether a key may call a model. The
// verdict is a method on Identity, and this guard keeps it that way: read from
// the production sources, a direct membership test on the allow-list or a
// length comparison used as the answer is the reimplementation that must not
// appear again.
//
// It is a source scan, and it is worth being honest about what that buys. It
// catches the two spellings the inverted default has actually taken here; it
// does not prove the absence of another, because a helper that copied the map
// and answered from the copy would read correctly. What the scan is really for
// is making the second reading expensive to reintroduce by accident, next to a
// method that already answers the question. The two rules it asserts - one
// entry point, and no length comparison - share a cause, so they share a test.
func TestTheAllowListVerdictHasOneEntryPointAndNoLengthComparison(t *testing.T) {
	root := filepath.Join("..", "..")
	lengthComparison := regexp.MustCompile(`len\([^)]*AllowedModels[^)]*\)\s*(==|!=|>=|<=|>|<)`)

	var membershipOutsideModel []string
	for _, file := range productionGoFiles(t, root) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("rel %s: %v", file, err)
		}
		rel = filepath.ToSlash(rel)
		text := strings.Join(codeLines(string(src)), "\n")

		if rel != "internal/model/types.go" && strings.Contains(text, "AllowedModels[") {
			membershipOutsideModel = append(membershipOutsideModel, rel)
		}
		if m := lengthComparison.FindString(text); m != "" {
			t.Errorf("%s compares the allow-list length (%q); the verdict must come from Identity.AllowsModel", rel, m)
		}
	}
	if len(membershipOutsideModel) > 0 {
		t.Fatalf("the allow-list is read directly in %v; membership must go through Identity.AllowsModel", membershipOutsideModel)
	}
}

// codeLines drops line comments so a source guard reacts to code rather than to
// prose. The model package's own documentation quotes the forbidden comparison
// while explaining why the method exists, and that sentence must not make the
// guard fire.
func codeLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if i := strings.Index(line, "//"); i >= 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			line = line[:i]
		}
		out = append(out, line)
	}
	return out
}

// productionGoFiles returns every non-test Go file under root, skipping the
// generated and vendored trees a source scan must not read.
func productionGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// A source scan reads this repository's sources and nothing else. A
			// working copy nested inside the tree - a second checkout, or a git
			// worktree created for a parallel branch - is a different repository
			// that merely happens to live here, and counting it makes the guard
			// fail for reasons that have nothing to do with this code. Dot
			// directories cover .git; the .git check covers a nested checkout
			// under an ordinary name.
			if path != root {
				if strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return fs.SkipDir
				}
			}
			switch d.Name() {
			case "node_modules", "dist", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatal("no production Go sources found; the guard would pass vacuously")
	}
	return out
}
