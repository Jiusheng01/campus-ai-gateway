package model

import "time"

// ProviderModel is an Upstream Model a Provider can serve: one entry in the
// channel's own list of what it offers. Routes choose from this list, but the
// list is not a Route — it only says the channel can do something, not that a
// client-facing model points at it (ADR-0018).
//
// Source records where the name came from, so the console can distinguish what
// the gateway suggested or discovered from what the operator typed. It is kept
// per row: a refresh merges names, it does not rewrite the provenance of a name
// that is already listed.
type ProviderModel struct {
	ID            string    `json:"id"`
	ProviderID    string    `json:"provider_id"`
	UpstreamModel string    `json:"upstream_model"`
	Source        string    `json:"source"`
	Status        int       `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Links is the management read projection of the Routes bound to this
	// (provider, upstream) pair. It is derived from gateway_model_route and is
	// not a second binding source. A candidate may be linked under more than one
	// external alias.
	Links []ProviderModelLink `json:"links,omitempty"`
}

type ProviderModelLink struct {
	RouteID     string `json:"route_id"`
	ModelID     string `json:"model_id"`
	Alias       string `json:"alias"`
	RouteStatus int    `json:"route_status"`
}

// The three sources a Provider Model can have.
const (
	// ProviderModelSourcePreset is seeded from the server-side supplier
	// catalog when a channel is created.
	ProviderModelSourcePreset = "preset"
	// ProviderModelSourceFetched is merged from the provider's own model list.
	ProviderModelSourceFetched = "fetched"
	// ProviderModelSourceManual is a name the operator added.
	ProviderModelSourceManual = "manual"
)

// CatalogProvider is one channel offering a Catalog Model. Opened says client
// traffic can already reach this channel for the name, which is narrower than
// the model's own Opened flag: a Virtual Model may exist with a Route to
// another channel only.
type CatalogProvider struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ProviderModelID string `json:"provider_model_id"`
	Opened          bool   `json:"opened"`
	ModelID         string `json:"model_id,omitempty"`
	RouteID         string `json:"route_id,omitempty"`
}

// CatalogModel is one row of the Model Catalog: a Provider Model name grouped
// across every channel that offers it (ADR-0018). Opened reports whether a
// Virtual Model with this alias exists; Providers reports which channels are
// already routed under it.
type CatalogModel struct {
	UpstreamModel string            `json:"upstream_model"`
	Opened        bool              `json:"opened"`
	Providers     []CatalogProvider `json:"providers"`
}

// CatalogOpenResult is the outcome of opening one Provider Model. Created is
// true only when the Virtual Model did not exist; RouteCreated is true only
// when the (model, provider, upstream) binding was new. Both false means the
// open was a repeat and changed nothing.
type CatalogOpenResult struct {
	Model        ModelConfig `json:"model"`
	Route        ModelRoute  `json:"route"`
	Created      bool        `json:"created"`
	RouteCreated bool        `json:"route_created"`
}
