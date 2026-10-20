package app

import (
	"net/url"
	"strings"
)

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

// providerPresetBaseURLs maps a normalised base URL — lower-case host, then the
// path without its leading or trailing slash — onto the catalog key.
//
// The base URL is the primary identifier because the channel name is only a
// form prefill: an operator who picks the DeepSeek template and then renames the
// channel still pointed it at DeepSeek's endpoint, and must still get DeepSeek's
// presets. A name that happens to match another supplier must not override the
// endpoint that was configured.
var providerPresetBaseURLs = map[string]string{
	"dashscope.aliyuncs.com/compatible-mode/v1": "qwen",
	"api.deepseek.com":                          "deepseek",
	"api.deepseek.com/v1":                       "deepseek",
	"api.openai.com/v1":                         "openai",
	"open.bigmodel.cn/api/paas/v4":              "glm",
	"api.moonshot.cn/v1":                        "kimi",
	"ark.cn-beijing.volces.com/api/v3":          "doubao",
}

// presetProviderModels returns a copy of the seed list for a supplier, or nil
// when the supplier is unknown. The base URL decides first and the name is the
// fallback. The copy matters: the seed is handed to storage and to callers, and
// no caller may mutate the catalog for the next channel.
func presetProviderModels(providerName, baseURL string) []string {
	key := presetKeyFromBaseURL(baseURL)
	if key == "" {
		key = presetKeyFromName(providerName)
	}
	names := providerModelPresets[key]
	if len(names) == 0 {
		return nil
	}
	return append([]string(nil), names...)
}

func presetKeyFromName(providerName string) string {
	key := strings.ToLower(strings.TrimSpace(providerName))
	if alias, ok := providerPresetAliases[key]; ok {
		return alias
	}
	return key
}

// presetKeyFromBaseURL normalises a configured base URL to the catalog key. A
// host is mandatory; the scheme, port, query and fragment do not identify the
// supplier, so they are not part of the key.
func presetKeyFromBaseURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	key := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if path := strings.ToLower(strings.Trim(u.Path, "/")); path != "" {
		key += "/" + path
	}
	return providerPresetBaseURLs[key]
}
