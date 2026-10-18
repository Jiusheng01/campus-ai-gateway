package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type thinkWatchTestRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type thinkWatchTestResult struct {
	Model            string `json:"model"`
	Channel          string `json:"channel,omitempty"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	LatencyMS        int64  `json:"latency_ms"`
}

func (s *Server) handleThinkWatchSync(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.cfg.UsesThinkWatch() {
		// Nothing to write to. Saying so beats "THINKWATCH_CONFIG_PATH is not
		// configured", which would send an operator looking for a file that
		// this deployment is never going to read.
		httpx.Error(w, http.StatusConflict, "data_plane_direct",
			"This gateway runs the direct data plane: routing is read from the database on every request, so there is no ThinkWatch configuration to synchronise", reqID)
		return
	}
	if strings.TrimSpace(s.cfg.ThinkWatchConfigPath) == "" {
		httpx.Error(w, http.StatusServiceUnavailable, "thinkwatch_config_path_missing", "THINKWATCH_CONFIG_PATH is not configured", reqID)
		return
	}

	providers, err := s.pg.ListProviders(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load providers", reqID)
		return
	}
	models, err := s.pg.ListModels(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load models", reqID)
		return
	}

	existing, err := os.ReadFile(s.cfg.ThinkWatchConfigPath)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "thinkwatch_config_unavailable", "ThinkWatch config could not be read; run twcore init or configure THINKWATCH_CONFIG_PATH", reqID)
		return
	}

	routesByModel := make(map[string][]model.ModelRoute, len(models))
	for _, m := range models {
		routes, err := s.pg.ListModelRoutes(r.Context(), m.ID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load model routes", reqID)
			return
		}
		routesByModel[m.ID] = routes
	}

	rendered, providerCount, aliasCount, err := renderThinkWatchManagedConfig(string(existing), s.cfg.ThinkWatchKey, providers, models, routesByModel)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "thinkwatch_config_invalid", err.Error(), reqID)
		return
	}

	if strings.TrimSpace(s.cfg.ThinkWatchExe) != "" {
		if err := validateThinkWatchConfig(s.cfg.ThinkWatchExe, s.cfg.ThinkWatchConfigPath, []byte(rendered)); err != nil {
			httpx.Error(w, http.StatusBadRequest, "thinkwatch_config_check_failed", err.Error(), reqID)
			return
		}
	}

	backupPath, err := replaceConfigWithBackup(s.cfg.ThinkWatchConfigPath, []byte(rendered))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "thinkwatch_config_write_failed", "Unable to update ThinkWatch config: "+err.Error(), reqID)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"config_path":    s.cfg.ThinkWatchConfigPath,
		"backup_path":    backupPath,
		"providers":      providerCount,
		"aliases":        aliasCount,
		"hot_reload_note": "ThinkWatch should detect the config change automatically; refresh status after about one second.",
	})
}

func (s *Server) handleModelTest(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if strings.TrimSpace(s.cfg.ThinkWatchKey) == "" && s.cfg.UsesThinkWatch() {
		httpx.Error(w, http.StatusServiceUnavailable, "thinkwatch_key_missing", "THINKWATCH_GATEWAY_KEY is not configured", reqID)
		return
	}

	var in thinkWatchTestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Model == "" || in.Prompt == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "model and prompt are required", reqID)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"model": in.Model,
		"messages": []map[string]string{
			{"role": "user", "content": in.Prompt},
		},
		"stream": false,
	})

	if !s.cfg.UsesThinkWatch() {
		s.serveModelTestDirect(w, r, in, payload, reqID)
		return
	}

	ctx := r.Context()
	upstreamURL := strings.TrimRight(s.cfg.ThinkWatchURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "test_request_failed", "Unable to create ThinkWatch test request", reqID)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.ThinkWatchKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "thinkwatch_unavailable", "ThinkWatch test request failed: "+err.Error(), reqID)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "thinkwatch_read_failed", "Unable to read ThinkWatch response", reqID)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if len(message) > 500 {
			message = message[:500]
		}
		httpx.Error(w, http.StatusBadGateway, "thinkwatch_test_failed", fmt.Sprintf("ThinkWatch returned HTTP %d: %s", resp.StatusCode, message), reqID)
		return
	}

	result, err := parseProbeResponse(body, time.Since(started))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "thinkwatch_response_invalid", "ThinkWatch returned invalid JSON", reqID)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// parseProbeResponse reduces an OpenAI-compatible completion to the fields the
// connectivity test reports.
//
// It is shared by both data planes on purpose: the operator-visible meaning of
// "the test passed" must not depend on which plane happens to be configured, so
// the token accounting and the content extraction are written once.
func parseProbeResponse(body []byte, latency time.Duration) (thinkWatchTestResult, error) {
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			InputTokens      int64 `json:"input_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return thinkWatchTestResult{}, err
	}

	result := thinkWatchTestResult{
		Model:       out.Model,
		InputTokens: out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
		TotalTokens: out.Usage.TotalTokens,
		LatencyMS:   latency.Milliseconds(),
	}
	if out.Usage.InputTokens > result.InputTokens {
		result.InputTokens = out.Usage.InputTokens
	}
	if out.Usage.OutputTokens > result.OutputTokens {
		result.OutputTokens = out.Usage.OutputTokens
	}
	if result.TotalTokens == 0 {
		result.TotalTokens = result.InputTokens + result.OutputTokens
	}
	if len(out.Choices) > 0 {
		result.Content = out.Choices[0].Message.Content
		result.ReasoningContent = out.Choices[0].Message.ReasoningContent
	}
	return result, nil
}

// managedModel pairs an enabled virtual model with its enabled routes.
type managedModel struct {
	config model.ModelConfig
	routes []model.ModelRoute
}

// renderThinkWatchManagedConfig renders the Gateway-managed top-level blocks of
// the ThinkWatch config while leaving everything else untouched.
//
// Since the generated `routes` block pins concrete provider/upstream-model
// pairs, ThinkWatch re-checks those pinned models against the calling gateway
// key's allow list. The `clients` block therefore has to stay in sync too:
// every enabled virtual alias and every upstream model used by an enabled route
// must be allowed for the Campus Gateway client, otherwise ThinkWatch rejects
// the request with "which gateway key ... may not use".
//
// gatewayKey identifies the Campus Gateway's ThinkWatch client. Only that
// client's allow list is widened, so unrelated clients keep their existing
// (minimal) permissions. If the key is unknown or matches no client, every
// client in the file is treated as a gateway client, because failing to widen
// the allow list would break the data plane.
func renderThinkWatchManagedConfig(existing, gatewayKey string, providers []model.Provider, models []model.ModelConfig, routesByModel map[string][]model.ModelRoute) (string, int, int, error) {
	activeProviders := make(map[string]model.Provider)
	for _, p := range providers {
		if p.Status != 1 {
			continue
		}
		if !envNamePattern.MatchString(strings.TrimSpace(p.CredentialEnv)) {
			return "", 0, 0, fmt.Errorf("provider %s has invalid credential_env", p.Name)
		}
		activeProviders[p.ID] = p
	}

	var managed []managedModel
	providerModels := make(map[string]map[string]bool)

	for _, m := range models {
		if m.Status != 1 {
			continue
		}
		var enabled []model.ModelRoute
		for _, route := range routesByModel[m.ID] {
			if route.Status != 1 {
				continue
			}
			p, ok := activeProviders[route.ProviderID]
			if !ok {
				continue
			}
			route.ProviderName = p.Name
			enabled = append(enabled, route)
		}
		// Transitional compatibility for a database upgraded before Route CRUD
		// was used. New and edited models always have gateway_model_route rows.
		if len(enabled) == 0 {
			if p, ok := activeProviders[m.ProviderID]; ok && strings.TrimSpace(m.UpstreamModel) != "" {
				enabled = append(enabled, model.ModelRoute{
					ModelID:       m.ID,
					ModelAlias:    m.Alias,
					ProviderID:    m.ProviderID,
					ProviderName:  p.Name,
					UpstreamModel: m.UpstreamModel,
					Priority:      1,
					Weight:        100,
					TimeoutMS:     60000,
					Status:        1,
				})
			}
		}
		if len(enabled) == 0 {
			continue
		}
		sort.SliceStable(enabled, func(i, j int) bool {
			if enabled[i].Priority != enabled[j].Priority {
				return enabled[i].Priority < enabled[j].Priority
			}
			if enabled[i].Weight != enabled[j].Weight {
				return enabled[i].Weight > enabled[j].Weight
			}
			if enabled[i].ProviderName != enabled[j].ProviderName {
				return enabled[i].ProviderName < enabled[j].ProviderName
			}
			return enabled[i].UpstreamModel < enabled[j].UpstreamModel
		})
		for _, route := range enabled {
			if providerModels[route.ProviderID] == nil {
				providerModels[route.ProviderID] = map[string]bool{}
			}
			providerModels[route.ProviderID][route.UpstreamModel] = true
		}
		managed = append(managed, managedModel{config: m, routes: enabled})
	}

	if len(managed) == 0 {
		return "", 0, 0, errors.New("no enabled models have an enabled route to an enabled provider; refusing to overwrite ThinkWatch routing configuration")
	}

	var activeProviderList []model.Provider
	for id, p := range activeProviders {
		if len(providerModels[id]) > 0 {
			activeProviderList = append(activeProviderList, p)
		}
	}
	sort.Slice(activeProviderList, func(i, j int) bool { return activeProviderList[i].Name < activeProviderList[j].Name })
	sort.Slice(managed, func(i, j int) bool { return managed[i].config.Alias < managed[j].config.Alias })

	var providerBlock strings.Builder
	providerBlock.WriteString("providers:\n")
	for _, p := range activeProviderList {
		providerBlock.WriteString("  - name: " + strconv.Quote(p.Name) + "\n")
		providerBlock.WriteString("    base_url: " + strconv.Quote(p.BaseURL) + "\n")
		providerBlock.WriteString("    protocol: " + strconv.Quote(p.Protocol) + "\n")
		providerBlock.WriteString("    key: " + strconv.Quote("${"+p.CredentialEnv+"}") + "\n")
		models := make([]string, 0, len(providerModels[p.ID]))
		for name := range providerModels[p.ID] {
			models = append(models, name)
		}
		sort.Strings(models)
		providerBlock.WriteString("    models:\n")
		for _, name := range models {
			providerBlock.WriteString("      - " + strconv.Quote(name) + "\n")
		}
		providerBlock.WriteString("    models_only:\n")
		for _, name := range models {
			providerBlock.WriteString("      - " + strconv.Quote(name) + "\n")
		}
	}

	var aliasBlock strings.Builder
	aliasBlock.WriteString("aliases:\n")
	for _, item := range managed {
		seen := map[string]bool{}
		var names []string
		for _, route := range item.routes {
			if !seen[route.UpstreamModel] {
				seen[route.UpstreamModel] = true
				names = append(names, route.UpstreamModel)
			}
		}
		if len(names) == 1 {
			aliasBlock.WriteString("  " + strconv.Quote(item.config.Alias) + ": " + strconv.Quote(names[0]) + "\n")
			continue
		}
		aliasBlock.WriteString("  " + strconv.Quote(item.config.Alias) + ":\n")
		for _, name := range names {
			aliasBlock.WriteString("    - " + strconv.Quote(name) + "\n")
		}
	}

	// ThinkWatch owns runtime routing/failover. We render each virtual model into
	// an ordered pinned target list, so different providers may use different
	// upstream model names while the client keeps one campus alias.
	var routeBlock strings.Builder
	routeBlock.WriteString("routes:\n")
	routeBlock.WriteString("  - name: " + strconv.Quote("campus-managed") + "\n")
	routeBlock.WriteString("    rules:\n")
	for _, item := range managed {
		routeBlock.WriteString("      - name: " + strconv.Quote("campus-"+item.config.Alias) + "\n")
		routeBlock.WriteString("        when:\n")
		routeBlock.WriteString("          model: " + strconv.Quote(item.config.Alias) + "\n")
		routeBlock.WriteString("        to:\n")
		for _, route := range item.routes {
			routeBlock.WriteString("          - provider: " + strconv.Quote(route.ProviderName) + "\n")
			routeBlock.WriteString("            model: " + strconv.Quote(route.UpstreamModel) + "\n")
		}
	}
	routeBlock.WriteString("      - name: " + strconv.Quote("campus-deny-unmanaged") + "\n")
	routeBlock.WriteString("        deny: " + strconv.Quote("model is not managed by Campus AI Gateway") + "\n")

	// ThinkWatch re-validates pinned route models against the calling gateway
	// key's allow list, so the enabled aliases and their upstream models must be
	// allowed for the Campus Gateway client.
	allowedModels := managedModelAllowList(managed)

	out := replaceTopLevelYAMLBlock(existing, "providers", strings.TrimRight(providerBlock.String(), "\n"))
	out = replaceTopLevelYAMLBlock(out, "aliases", strings.TrimRight(aliasBlock.String(), "\n"))
	out = replaceTopLevelYAMLBlock(out, "routes", strings.TrimRight(routeBlock.String(), "\n"))
	out = replaceTopLevelYAMLBlock(out, "default_route", "default_route: "+strconv.Quote("campus-managed"))
	out = syncThinkWatchClientAllow(out, gatewayKey, allowedModels)
	return out, len(activeProviderList), len(managed), nil
}

// managedModelAllowList returns the deduplicated, sorted set of model names that
// must be allowed for the Campus Gateway's ThinkWatch client: every enabled
// virtual alias plus every upstream model used by an enabled route.
func managedModelAllowList(managed []managedModel) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range managed {
		if alias := strings.TrimSpace(item.config.Alias); alias != "" && !seen[alias] {
			seen[alias] = true
			out = append(out, alias)
		}
		for _, route := range item.routes {
			model := strings.TrimSpace(route.UpstreamModel)
			if model != "" && !seen[model] {
				seen[model] = true
				out = append(out, model)
			}
		}
	}
	sort.Strings(out)
	return out
}

func replaceTopLevelYAMLBlock(content, key, block string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	start, end := findTopLevelYAMLBlock(lines, key)

	if start >= 0 {
		replacement := strings.Split(block, "\n")
		lines = append(append(append([]string{}, lines[:start]...), replacement...), lines[end:]...)
		return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	}

	base := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if base != "" {
		base += "\n"
	}
	return base + block + "\n"
}

// findTopLevelYAMLBlock returns the half-open line range of a top-level YAML
// block, or (-1, -1) when the key is absent.
func findTopLevelYAMLBlock(lines []string, key string) (int, int) {
	start := -1
	for i, line := range lines {
		if isTopLevelYAMLKey(line, key) {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if isAnyTopLevelYAMLKey(lines[i]) {
			end = i
			break
		}
	}
	return start, end
}

func isTopLevelYAMLKey(line, key string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	return strings.HasPrefix(trimmed, key+":")
}

func isAnyTopLevelYAMLKey(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	idx := strings.IndexByte(trimmed, ':')
	if idx <= 0 {
		return false
	}
	for _, r := range trimmed[:idx] {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// syncThinkWatchClientAllow widens the allow list of the Campus Gateway's
// ThinkWatch client so the pinned route models stay reachable. It never
// rewrites client keys, never drops existing allow entries and never touches
// unrelated top-level blocks.
func syncThinkWatchClientAllow(config, gatewayKey string, managed []string) string {
	if len(managed) == 0 {
		return config
	}
	config = strings.ReplaceAll(config, "\r\n", "\n")
	lines := strings.Split(config, "\n")
	start, end := findTopLevelYAMLBlock(lines, "clients")
	if start < 0 {
		return config
	}

	block := lines[start:end]
	rewritten := rewriteClientAllowLists(block, gatewayKey, managed)
	if rewritten == nil {
		return config
	}

	merged := make([]string, 0, len(lines))
	merged = append(merged, lines[:start]...)
	merged = append(merged, rewritten...)
	merged = append(merged, lines[end:]...)
	return strings.TrimRight(strings.Join(merged, "\n"), "\n") + "\n"
}

// rewriteClientAllowLists rewrites only the `allow` list of each gateway client
// inside a `clients` block. It returns nil when the block has no client entry.
func rewriteClientAllowLists(block []string, gatewayKey string, managed []string) []string {
	indentOf := func(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
	isItem := func(s string) bool {
		t := strings.TrimLeft(s, " \t")
		return strings.HasPrefix(t, "- ") || t == "-"
	}

	clientIndent := -1
	for _, line := range block {
		if isItem(line) {
			if ind := indentOf(line); clientIndent == -1 || ind < clientIndent {
				clientIndent = ind
			}
		}
	}
	if clientIndent == -1 {
		return nil
	}

	// Split the block into contiguous client entries.
	type span struct{ start, end int }
	var spans []span
	open := -1
	for i, line := range block {
		if isItem(line) && indentOf(line) == clientIndent {
			if open >= 0 {
				spans = append(spans, span{open, i})
			}
			open = i
		}
	}
	if open >= 0 {
		spans = append(spans, span{open, len(block)})
	}
	if len(spans) == 0 {
		return nil
	}

	// Only the Campus Gateway client should be widened. When the key is unknown
	// or matches nothing, fall back to every client so the data plane keeps
	// working instead of silently rejecting pinned route models.
	gateway := make([]bool, len(spans))
	matched := false
	for i, sp := range spans {
		if gatewayKey != "" && clientKeyFromLines(block[sp.start:sp.end]) == gatewayKey {
			gateway[i] = true
			matched = true
		}
	}
	if gatewayKey == "" || !matched {
		for i := range gateway {
			gateway[i] = true
		}
	}

	out := append([]string{}, block[:spans[0].start]...)
	for i, sp := range spans {
		segment := block[sp.start:sp.end]
		if gateway[i] {
			segment = mergeClientAllow(segment, clientIndent, managed)
		}
		out = append(out, segment...)
	}
	return out
}

// clientKeyFromLines extracts the `key` value of a single client entry.
func clientKeyFromLines(segment []string) string {
	for _, line := range segment {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "key:") {
			continue
		}
		return yamlScalarValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "key:")))
	}
	return ""
}

// mergeClientAllow returns the client entry with its allow list widened by the
// managed model names. Existing entries keep their original order and literal
// spelling; new entries are appended in sorted order.
func mergeClientAllow(segment []string, clientIndent int, managed []string) []string {
	fieldIndent := clientIndent + 2
	for i := 1; i < len(segment); i++ {
		if strings.TrimSpace(segment[i]) == "" {
			continue
		}
		fieldIndent = len(segment[i]) - len(strings.TrimLeft(segment[i], " \t"))
		break
	}
	fieldPad := strings.Repeat(" ", fieldIndent)
	itemPad := strings.Repeat(" ", fieldIndent+2)

	allowIdx := -1
	inline := ""
	for i, line := range segment {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "allow:") {
			allowIdx = i
			inline = strings.TrimSpace(strings.TrimPrefix(trimmed, "allow:"))
			break
		}
	}

	var existing []allowEntry
	allowIndent := fieldIndent
	consumeTo := allowIdx + 1
	if allowIdx >= 0 {
		allowIndent = len(segment[allowIdx]) - len(strings.TrimLeft(segment[allowIdx], " \t"))
		if strings.HasPrefix(inline, "[") {
			body := strings.TrimSuffix(strings.TrimPrefix(inline, "["), "]")
			for _, part := range strings.Split(body, ",") {
				if e, ok := parseAllowEntry(strings.TrimSpace(part)); ok {
					existing = append(existing, e)
				}
			}
		} else {
			for i := allowIdx + 1; i < len(segment); i++ {
				line := segment[i]
				if strings.TrimSpace(line) == "" {
					break
				}
				if len(line)-len(strings.TrimLeft(line, " \t")) <= allowIndent {
					break
				}
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "- ") {
					break
				}
				if e, ok := parseAllowEntry(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))); ok {
					existing = append(existing, e)
				}
				consumeTo = i + 1
			}
		}
	}

	values, raw := mergeAllowValues(existing, managed)

	rendered := make([]string, 0, len(values)+2)
	if allowIdx < 0 {
		// No allow list yet: append one at the end of the client entry.
		end := len(segment)
		for end > 0 && strings.TrimSpace(segment[end-1]) == "" {
			end--
		}
		rendered = append(rendered, segment[:end]...)
		rendered = append(rendered, fieldPad+"allow:")
		for _, r := range raw {
			rendered = append(rendered, itemPad+"- "+r)
		}
		rendered = append(rendered, segment[end:]...)
		return rendered
	}

	rendered = append(rendered, segment[:allowIdx]...)
	rendered = append(rendered, fieldPad+"allow:")
	for _, r := range raw {
		rendered = append(rendered, itemPad+"- "+r)
	}
	rendered = append(rendered, segment[consumeTo:]...)
	return rendered
}

type allowEntry struct {
	value string
	raw   string
}

func parseAllowEntry(token string) (allowEntry, bool) {
	if token == "" || token == "[]" {
		return allowEntry{}, false
	}
	value := yamlScalarValue(token)
	if value == "" {
		return allowEntry{}, false
	}
	return allowEntry{value: value, raw: token}, true
}

// mergeAllowValues keeps existing entries (original order and spelling) and
// appends the managed models that are still missing, sorted for stable diffs.
func mergeAllowValues(existing []allowEntry, managed []string) ([]string, []string) {
	seen := map[string]bool{}
	var values, raw []string
	for _, e := range existing {
		if seen[e.value] {
			continue
		}
		seen[e.value] = true
		values = append(values, e.value)
		raw = append(raw, e.raw)
	}

	var missing []string
	for _, m := range managed {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		missing = append(missing, m)
	}
	sort.Strings(missing)
	for _, m := range missing {
		values = append(values, m)
		raw = append(raw, strconv.Quote(m))
	}
	return values, raw
}

// yamlScalarValue strips the optional surrounding quotes of a YAML scalar.
func yamlScalarValue(token string) string {
	token = strings.TrimSpace(token)
	if len(token) >= 2 {
		if (token[0] == '"' && token[len(token)-1] == '"') || (token[0] == '\'' && token[len(token)-1] == '\'') {
			return token[1 : len(token)-1]
		}
	}
	return token
}

func validateThinkWatchConfig(exePath, configPath string, content []byte) error {
	if _, err := os.Stat(exePath); err != nil {
		return fmt.Errorf("twcore executable is unavailable: %w", err)
	}

	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, "config.campus-check-*.yaml")
	if err != nil {
		return fmt.Errorf("create validation config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write validation config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close validation config: %w", err)
	}

	cmd := exec.Command(exePath, "--config", tmpPath, "check")
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ThinkWatch rejected generated config: %s", msg)
	}
	return nil
}

func replaceConfigWithBackup(path string, content []byte) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, "config.campus-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	backupPath := path + ".campus.bak"
	original, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(backupPath, original, info.Mode().Perm()); err != nil {
		return "", err
	}

	if err := os.Remove(path); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.WriteFile(path, original, info.Mode().Perm())
		return "", err
	}
	return backupPath, nil
}
