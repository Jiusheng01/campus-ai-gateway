package app

import "strings"

// providerModelPresets is the server-side supplier catalog: the common upstream
// model names a channel of that supplier usually offers.
//
// It lives here, next to the channel write path, rather than in the console's
// template file. The console is not the only client — a channel can be created
// by any admin API caller — and seeding is a property of the channel, so the
// authority for the seed has to be the server. The keys mirror the console's
// template identifiers so the two stay recognisably the same catalog, but the
// server does not read the frontend's copy.
//
// The lists are deliberately short and conservative: they are a starting point
// an operator can extend manually or replace with "从上游刷新", not an
// authoritative inventory of what the vendor sells today.
var providerModelPresets = map[string][]string{
	"qwen": {
		"qwen-max",
		"qwen-plus",
		"qwen-turbo",
		"qwen-long",
		"qwen-vl-max",
	},
	"deepseek": {
		"deepseek-chat",
		"deepseek-reasoner",
	},
	"openai": {
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
		"o3",
		"o4-mini",
	},
	"glm": {
		"glm-4.5",
		"glm-4.5-air",
		"glm-4-plus",
		"glm-4-flash",
		"glm-4v-plus",
	},
	"kimi": {
		"kimi-k2-0711-preview",
		"moonshot-v1-8k",
		"moonshot-v1-32k",
		"moonshot-v1-128k",
	},
	"doubao": {
		"doubao-seed-1-6-250615",
		"doubao-1-5-pro-32k-250115",
		"doubao-1-5-lite-32k-250115",
	},
}

// providerPresetAliases maps the extra identifiers the console recognises a
// supplier by onto the catalog key.
var providerPresetAliases = map[string]string{
	"volcengine-ark": "doubao",
	"ark":            "doubao",
}

// presetProviderModels returns a copy of the seed list for a supplier, or nil
// when the supplier is unknown. The copy matters: the seed is handed to storage
// and to callers, and no caller may mutate the catalog for the next channel.
func presetProviderModels(providerName string) []string {
	key := strings.ToLower(strings.TrimSpace(providerName))
	if alias, ok := providerPresetAliases[key]; ok {
		key = alias
	}
	names := providerModelPresets[key]
	if len(names) == 0 {
		return nil
	}
	return append([]string(nil), names...)
}
