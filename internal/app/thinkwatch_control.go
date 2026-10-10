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

	rendered, providerCount, aliasCount, err := renderThinkWatchManagedConfig(string(existing), providers, models)
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
	if strings.TrimSpace(s.cfg.ThinkWatchKey) == "" {
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
		httpx.Error(w, http.StatusBadGateway, "thinkwatch_response_invalid", "ThinkWatch returned invalid JSON", reqID)
		return
	}

	result := thinkWatchTestResult{
		Model:       out.Model,
		InputTokens: out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
		TotalTokens: out.Usage.TotalTokens,
		LatencyMS:   time.Since(started).Milliseconds(),
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

	writeJSON(w, http.StatusOK, result)
}

func renderThinkWatchManagedConfig(existing string, providers []model.Provider, models []model.ModelConfig) (string, int, int, error) {
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

	modelsByProvider := make(map[string][]model.ModelConfig)
	var activeModels []model.ModelConfig
	for _, m := range models {
		if m.Status != 1 {
			continue
		}
		if _, ok := activeProviders[m.ProviderID]; !ok {
			continue
		}
		modelsByProvider[m.ProviderID] = append(modelsByProvider[m.ProviderID], m)
		activeModels = append(activeModels, m)
	}

	if len(activeModels) == 0 {
		return "", 0, 0, errors.New("no enabled models are attached to enabled providers; refusing to overwrite ThinkWatch provider configuration")
	}

	var activeProviderList []model.Provider
	for id, p := range activeProviders {
		if len(modelsByProvider[id]) > 0 {
			activeProviderList = append(activeProviderList, p)
		}
	}
	sort.Slice(activeProviderList, func(i, j int) bool { return activeProviderList[i].Name < activeProviderList[j].Name })
	sort.Slice(activeModels, func(i, j int) bool { return activeModels[i].Alias < activeModels[j].Alias })

	var providerBlock strings.Builder
	providerBlock.WriteString("providers:\n")
	for _, p := range activeProviderList {
		ms := modelsByProvider[p.ID]
		sort.Slice(ms, func(i, j int) bool { return ms[i].UpstreamModel < ms[j].UpstreamModel })
		providerBlock.WriteString("  - name: " + strconv.Quote(p.Name) + "\n")
		providerBlock.WriteString("    base_url: " + strconv.Quote(p.BaseURL) + "\n")
		providerBlock.WriteString("    protocol: " + strconv.Quote(p.Protocol) + "\n")
		providerBlock.WriteString("    key: " + strconv.Quote("${"+p.CredentialEnv+"}") + "\n")
		providerBlock.WriteString("    models:\n")
		seen := map[string]bool{}
		for _, m := range ms {
			if seen[m.UpstreamModel] {
				continue
			}
			seen[m.UpstreamModel] = true
			providerBlock.WriteString("      - " + strconv.Quote(m.UpstreamModel) + "\n")
		}
		providerBlock.WriteString("    models_only:\n")
		for _, m := range ms {
			if !seen[m.UpstreamModel] {
				continue
			}
			providerBlock.WriteString("      - " + strconv.Quote(m.UpstreamModel) + "\n")
			delete(seen, m.UpstreamModel)
		}
	}

	var aliasBlock strings.Builder
	aliasBlock.WriteString("aliases:\n")
	for _, m := range activeModels {
		aliasBlock.WriteString("  " + strconv.Quote(m.Alias) + ": " + strconv.Quote(m.UpstreamModel) + "\n")
	}

	out := replaceTopLevelYAMLBlock(existing, "providers", strings.TrimRight(providerBlock.String(), "\n"))
	out = replaceTopLevelYAMLBlock(out, "aliases", strings.TrimRight(aliasBlock.String(), "\n"))
	return out, len(activeProviderList), len(activeModels), nil
}

func replaceTopLevelYAMLBlock(content, key, block string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	start := -1
	end := len(lines)

	for i, line := range lines {
		if isTopLevelYAMLKey(line, key) {
			start = i
			break
		}
	}
	if start >= 0 {
		for i := start + 1; i < len(lines); i++ {
			if isAnyTopLevelYAMLKey(lines[i]) {
				end = i
				break
			}
		}
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
