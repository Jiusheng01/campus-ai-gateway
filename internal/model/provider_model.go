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
