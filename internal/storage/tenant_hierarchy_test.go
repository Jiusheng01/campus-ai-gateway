package storage

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// ptr is a tiny helper for the three-state quota fields.

// --- path helpers ------------------------------------------------------------

func TestParseTenantPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"root", "/a/", []string{"a"}},
		{"nested", "/a/b/c/", []string{"a", "b", "c"}},
		{"missing trailing slash", "/a/b", []string{"a", "b"}},
		{"empty", "", []string{}},
		{"only slashes", "///", []string{}},
		{"double separators are collapsed", "/a//b/", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseTenantPath(tc.path); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseTenantPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestBuildTenantPath(t *testing.T) {
	// The parent argument is a stored path, so it always carries both
	// separators.
	root := "/root-id/"
	nested := "/root-id/parent-id/"

	if got := BuildTenantPath(nil, "new-id"); got != "/new-id/" {
		t.Fatalf("nil parent = %q, want %q", got, "/new-id/")
	}
	if got := BuildTenantPath(&root, "new-id"); got != "/root-id/new-id/" {
		t.Fatalf("root parent = %q, want %q", got, "/root-id/new-id/")
	}
	if got := BuildTenantPath(&nested, "new-id"); got != "/root-id/parent-id/new-id/" {
		t.Fatalf("nested parent = %q, want %q", got, "/root-id/parent-id/new-id/")
	}

	empty := ""
	if got := BuildTenantPath(&empty, "new-id"); got != "/new-id/" {
		t.Fatalf("empty parent = %q, want %q", got, "/new-id/")
	}
}

// The round trip is what makes the path usable as a scope check.
func TestBuildAndParseTenantPathRoundTrip(t *testing.T) {
	path := BuildTenantPath(nil, "root")
	path = BuildTenantPath(&path, "dept")
	path = BuildTenantPath(&path, "student")

	want := []string{"root", "dept", "student"}
	if got := ParseTenantPath(path); !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %v, want %v", got, want)
	}
	if got := TenantDepthFromPath(path); got != 2 {
		t.Fatalf("depth = %d, want 2", got)
	}
}

func TestTenantSubtreePrefixMatchesTheNodeAndItsDescendants(t *testing.T) {
	// "/a/b/" + "%" is the LIKE pattern that matches "/a/b/" itself and
	// everything under it.
	prefix := TenantSubtreePrefix("/a/b/")
	if prefix != "/a/b/%" {
		t.Fatalf("prefix = %q, want %q", prefix, "/a/b/%")
	}
}

func TestTenantDepthFromPath(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"/a/", 0},
		{"/a/b/", 1},
		{"/a/b/c/d/", 3},
		{"", 0},
	}
	for _, tc := range cases {
		if got := TenantDepthFromPath(tc.path); got != tc.want {
			t.Fatalf("TenantDepthFromPath(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

// --- quota resolution --------------------------------------------------------

func tenant(id string, daily *int64) model.Tenant {
	return model.Tenant{ID: id, DailyTokenLimit: daily}
}

// ResolveQuotas keeps each level's own declaration. It deliberately does NOT
// fold inheritance: a counter is inclusive, so a parent's counter already
// contains its children's usage, and checking an inherited value at every level
// would charge the same usage once per level -- and would turn an explicit 0
// ("unlimited here") into the parent's number. The collapse into a single
// "effective" number happens in ResolveEffectiveLimits, which is display only.
func TestResolveQuotasKeepsEachLevelsOwnDeclaration(t *testing.T) {
	chain := []model.Tenant{
		tenant("a", i64(1000)),
		tenant("b", nil),
		tenant("c", i64(0)),
	}
	got := ResolveQuotas(chain)
	if len(got) != 3 {
		t.Fatalf("got %d levels, want 3", len(got))
	}
	if got[0].DailyTokenLimit == nil || *got[0].DailyTokenLimit != 1000 {
		t.Fatalf("root daily = %v, want 1000", got[0].DailyTokenLimit)
	}
	if got[1].DailyTokenLimit != nil {
		t.Fatalf("middle daily = %v, want nil (it declares nothing)", got[1].DailyTokenLimit)
	}
	if got[2].DailyTokenLimit == nil || *got[2].DailyTokenLimit != 0 {
		t.Fatalf("leaf daily = %v, want an explicit 0", got[2].DailyTokenLimit)
	}
}

// Effective limits are the display-side view: the nearest declaration walking
// down from the root. This is where inheritance actually collapses, and the
// subtle case is an explicit 0 -- it is a value, so it stops the walk rather
// than being skipped as an absence.
func TestResolveEffectiveLimitsInheritsFromTheNearestAncestor(t *testing.T) {
	cases := []struct {
		name  string
		chain []model.Tenant
		want  int64
	}{
		{
			name:  "all unset resolves to unlimited",
			chain: []model.Tenant{tenant("a", nil), tenant("b", nil)},
			want:  0,
		},
		{
			name:  "a root value flows down to the leaf",
			chain: []model.Tenant{tenant("a", i64(1000)), tenant("b", nil), tenant("c", nil)},
			want:  1000,
		},
		{
			name:  "a child overrides its own subtree only",
			chain: []model.Tenant{tenant("a", i64(1000)), tenant("b", i64(200)), tenant("c", nil)},
			want:  200,
		},
		{
			name:  "a middle value does not affect the root",
			chain: []model.Tenant{tenant("a", nil), tenant("b", i64(300)), tenant("c", nil)},
			want:  300,
		},
		{
			name:  "an explicit zero stops inheritance",
			chain: []model.Tenant{tenant("a", i64(1000)), tenant("b", i64(0)), tenant("c", nil)},
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEffectiveLimits(tc.chain).DailyTokenLimit
			if got != tc.want {
				t.Fatalf("effective daily = %d, want %d", got, tc.want)
			}
		})
	}
}

// The resolved chain keeps the tenant id, which is what lets the limiter name
// the level that rejected a request.
func TestResolveQuotasKeepsTenantIDsInRootFirstOrder(t *testing.T) {
	chain := []model.Tenant{tenant("root", i64(10)), tenant("dept", nil), tenant("student", i64(1))}

	got := ResolveQuotas(chain)
	ids := make([]string, 0, len(got))
	for _, q := range got {
		ids = append(ids, q.TenantID)
	}
	if want := []string{"root", "dept", "student"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("tenant ids = %v, want %v", ids, want)
	}
}

// --- access resolution -------------------------------------------------------

func accessTenant(id string, models []string) model.Tenant {
	return model.Tenant{ID: id, AllowedModels: models}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestResolveAccessIntersectsInsteadOfInheriting(t *testing.T) {
	cases := []struct {
		name           string
		chain          []model.Tenant
		wantModels     []string
		wantRestricted bool
	}{
		{
			name:           "no allowlist anywhere means unrestricted",
			chain:          []model.Tenant{accessTenant("a", nil), accessTenant("b", nil)},
			wantModels:     []string{},
			wantRestricted: false,
		},
		{
			name:           "a single allowlist applies to the whole subtree",
			chain:          []model.Tenant{accessTenant("a", []string{"gpt-4o", "claude"}), accessTenant("b", nil)},
			wantModels:     []string{"claude", "gpt-4o"},
			wantRestricted: true,
		},
		{
			// The security property: a child cannot widen what a parent allows.
			name:           "a child can only narrow the parent's list",
			chain:          []model.Tenant{accessTenant("a", []string{"gpt-4o", "claude"}), accessTenant("b", []string{"claude", "llama"})},
			wantModels:     []string{"claude"},
			wantRestricted: true,
		},
		{
			name:           "disjoint lists deny everything",
			chain:          []model.Tenant{accessTenant("a", []string{"gpt-4o"}), accessTenant("b", []string{"claude"})},
			wantModels:     []string{},
			wantRestricted: true,
		},
		{
			name:           "an empty list is deny-all, not inherit",
			chain:          []model.Tenant{accessTenant("a", nil), accessTenant("b", []string{})},
			wantModels:     []string{},
			wantRestricted: true,
		},
		{
			name:           "a root deny-all is not widened by a child list",
			chain:          []model.Tenant{accessTenant("a", []string{}), accessTenant("b", []string{"gpt-4o"})},
			wantModels:     []string{},
			wantRestricted: true,
		},
		{
			name:           "three levels intersect transitively",
			chain:          []model.Tenant{accessTenant("a", []string{"a", "b", "c"}), accessTenant("b", []string{"b", "c", "d"}), accessTenant("c", []string{"c", "d", "e"})},
			wantModels:     []string{"c"},
			wantRestricted: true,
		},
		{
			name:           "empty chain is unrestricted",
			chain:          []model.Tenant{},
			wantModels:     []string{},
			wantRestricted: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, restricted := ResolveAccess(tc.chain)
			if restricted != tc.wantRestricted {
				t.Fatalf("restricted = %v, want %v", restricted, tc.wantRestricted)
			}
			got := sortedKeys(set)
			if !reflect.DeepEqual(got, tc.wantModels) {
				t.Fatalf("models = %v, want %v", got, tc.wantModels)
			}
		})
	}
}

// An unrestricted result must be distinguishable from an empty allowlist: the
// former means "every model", the latter "no model".
func TestResolveAccessEmptyAllowlistIsNotUnrestricted(t *testing.T) {
	set, restricted := ResolveAccess([]model.Tenant{accessTenant("a", []string{})})
	if !restricted {
		t.Fatal("an explicit empty allowlist must be reported as restricted")
	}
	if set == nil {
		t.Fatal("an empty allowlist must still be a usable (non-nil) set")
	}
	if len(set) != 0 {
		t.Fatalf("expected an empty set, got %v", sortedKeys(set))
	}
}

// --- set helpers -------------------------------------------------------------

func TestStringSet(t *testing.T) {
	if got := StringSet(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil input must yield a non-nil empty set, got %v", got)
	}
	if got := StringSet([]string{}); got == nil || len(got) != 0 {
		t.Fatalf("empty input must yield a non-nil empty set, got %v", got)
	}
	got := StringSet([]string{"a", "b", "a"})
	if len(got) != 2 {
		t.Fatalf("duplicates must collapse, got %v", sortedKeys(got))
	}
}

func TestIntersectSets(t *testing.T) {
	a := StringSet([]string{"a", "b", "c"})
	b := StringSet([]string{"b", "c", "d"})

	got := sortedKeys(IntersectSets(a, b))
	if want := []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intersection = %v, want %v", got, want)
	}
	// The function swaps its arguments internally to iterate the smaller set;
	// the result must not depend on the order.
	reversed := sortedKeys(IntersectSets(b, a))
	if !reflect.DeepEqual(reversed, got) {
		t.Fatalf("intersection is not commutative: %v vs %v", got, reversed)
	}
	if res := IntersectSets(StringSet([]string{"a"}), StringSet([]string{"b"})); len(res) != 0 || res == nil {
		t.Fatalf("disjoint sets must yield a non-nil empty set, got %v", res)
	}
}

func TestComposeAccess(t *testing.T) {
	key := StringSet([]string{"a", "b"})
	tenantSet := StringSet([]string{"b", "c"})

	cases := []struct {
		name           string
		aSet           map[string]struct{}
		aRestricted    bool
		bSet           map[string]struct{}
		bRestricted    bool
		wantModels     []string
		wantRestricted bool
	}{
		{"neither side constrains", nil, false, nil, false, []string{}, false},
		{"only the key constrains", key, true, nil, false, []string{"a", "b"}, true},
		{"only the tenant constrains", nil, false, tenantSet, true, []string{"b", "c"}, true},
		{"both constrain, so they intersect", key, true, tenantSet, true, []string{"b"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, restricted := ComposeAccess(tc.aSet, tc.aRestricted, tc.bSet, tc.bRestricted)
			if restricted != tc.wantRestricted {
				t.Fatalf("restricted = %v, want %v", restricted, tc.wantRestricted)
			}
			if got := sortedKeys(set); !reflect.DeepEqual(got, tc.wantModels) {
				t.Fatalf("models = %v, want %v", got, tc.wantModels)
			}
		})
	}
}

// --- effective limits --------------------------------------------------------

// The flattened view is what an administrator reads back after allocating. It
// has to agree with ResolveQuotas and ResolveAccess, because a discrepancy
// between "what I allocated" and "what it says it has" is the bug this whole
// reporting surface exists to prevent.
func TestResolveEffectiveLimitsFlattensBothAxes(t *testing.T) {
	chain := []model.Tenant{
		{
			ID:                  "school",
			DailyTokenLimit:     i64(1000),
			MonthlyTokenLimit:   i64(10000),
			MonthlyRequestLimit: i64(500),
			MonthlyBudgetMicros: 900,
			AllowedModels:       []string{"a", "b", "c"},
			Path:                "/school/",
			Depth:               0,
		},
		{
			ID:            "dept",
			AllowedModels: []string{"b", "c"},
			Path:          "/school/dept/",
			Depth:         1,
		},
		{
			ID:    "teacher",
			Path:  "/school/dept/teacher/",
			Depth: 2,
		},
	}

	got := ResolveEffectiveLimits(chain)

	if got.TenantID != "teacher" {
		t.Fatalf("tenant id = %q, want %q", got.TenantID, "teacher")
	}
	if got.DailyTokenLimit != 1000 || got.MonthlyTokenLimit != 10000 ||
		got.MonthlyRequestLimit != 500 || got.MonthlyBudgetMicros != 900 {
		t.Fatalf("quota did not inherit down the chain: %+v", got)
	}
	if got.Path != "/school/dept/teacher/" || got.Depth != 2 {
		t.Fatalf("position = %q depth %d, want /school/dept/teacher/ depth 2", got.Path, got.Depth)
	}
	if !got.ModelAccessRestricted {
		t.Fatal("an allowlist applies, so the result must be marked restricted")
	}
	if want := []string{"b", "c"}; !reflect.DeepEqual(got.AllowedModels, want) {
		t.Fatalf("models = %v, want %v (the intersection of every level)", got.AllowedModels, want)
	}
}

func TestResolveEffectiveLimitsOnAnUnrestrictedChain(t *testing.T) {
	chain := []model.Tenant{
		{ID: "school", Path: "/school/"},
		{ID: "teacher", Path: "/school/teacher/", Depth: 1},
	}

	got := ResolveEffectiveLimits(chain)
	if got.ModelAccessRestricted {
		t.Fatal("no allowlist anywhere means unrestricted")
	}
	if got.AllowedModels == nil || len(got.AllowedModels) != 0 {
		t.Fatalf("models = %v, want a non-nil empty list", got.AllowedModels)
	}
	// 0 means unlimited, and an unset quota resolves to 0.
	if got.DailyTokenLimit != 0 || got.MonthlyRequestLimit != 0 {
		t.Fatalf("unset limits must resolve to 0 (unlimited), got %+v", got)
	}
}

func TestResolveEffectiveLimitsEmptyChain(t *testing.T) {
	got := ResolveEffectiveLimits(nil)
	if got.TenantID != "" || got.ModelAccessRestricted {
		t.Fatalf("an empty chain must resolve to the zero value, got %+v", got)
	}
}

// --- encoding helpers --------------------------------------------------------

// The JSONB column has to keep three states apart, which is the whole reason
// the allowlist is a pointer-like nil slice rather than a plain slice.
func TestMarshalModelsPreservesTheThreeStates(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  any
	}{
		{"nil inherits", nil, nil},
		{"empty denies everything", []string{}, "[]"},
		{"a list is encoded as JSON", []string{"gpt-4o"}, `["gpt-4o"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := marshalModels(tc.input); got != tc.want {
				t.Fatalf("marshalModels(%v) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}
