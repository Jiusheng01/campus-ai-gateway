package app

import (
	"net/http"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// handleOnboarding serves GET /admin/onboarding.
//
// The dashboard's setup checklist is derived from what the deployment actually
// contains, not from a stored "wizard completed" flag. That is what lets it say
// "the model exists but has no route" instead of pointing at a tutorial, and it
// reopens a step when its last row is removed.
//
// Only global administrators reach this handler; a scoped tenant_admin is
// refused in the RBAC allow-list because every count here is deployment-wide.
func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	principal, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}

	counts, err := s.pg.OnboardingCounts(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load setup state", reqID)
		return
	}

	writeJSON(w, http.StatusOK, buildOnboardingGuide(principal.Roles[RoleSuperAdmin], counts))
}

// buildOnboardingGuide returns the steps the caller can actually complete.
//
// gateway_admin may configure channels, models and routes but may not create
// tenants or API keys, so showing it the key and first-request steps would be a
// checklist with items it is forbidden to finish. super_admin owns the whole
// chain and gets all six.
func buildOnboardingGuide(superAdmin bool, c model.OnboardingCounts) model.OnboardingGuide {
	steps := []model.OnboardingStep{
		{
			ID:          "provider",
			Label:       "配置渠道",
			Description: "至少启用一个上游渠道，网关才有可以调用的模型服务。",
			Done:        c.EnabledProviders > 0,
			Count:       c.EnabledProviders,
			Action:      "providers",
		},
		{
			ID:          "model",
			Label:       "创建对外模型",
			Description: "模型目录里的别名就是业务调用时填写的 model 名称，创建后不可修改。",
			Done:        c.EnabledModels > 0,
			Count:       c.EnabledModels,
			Action:      "models",
		},
		{
			ID:          "route",
			Label:       "添加线路",
			Description: "为模型至少添加一条启用线路；相同优先级按权重分流，整组失败后切到下一组。",
			Done:        c.RoutableModels > 0,
			Count:       c.RoutableModels,
			Action:      "models",
		},
	}
	if superAdmin {
		steps = append(steps,
			model.OnboardingStep{
				ID:          "tenant",
				Label:       "创建租户",
				Description: "租户承载额度与密钥；不同院系或系统分开建租户，才能分别限额。",
				Done:        c.Tenants > 0,
				Count:       c.Tenants,
				Action:      "tenants",
			},
			model.OnboardingStep{
				ID:          "key",
				Label:       "创建 API 密钥",
				Description: "为业务系统创建密钥，并按租户限定可调用的模型范围。",
				Done:        c.EnabledAPIKeys > 0,
				Count:       c.EnabledAPIKeys,
				Action:      "keys",
			},
			model.OnboardingStep{
				ID:          "request",
				Label:       "发送首个请求",
				Description: "用密钥调用一次 /v1/chat/completions，并在使用日志里确认链路打通。",
				Done:        c.Requests > 0,
				Count:       c.Requests,
			},
		)
	}

	complete := len(steps) > 0
	for _, step := range steps {
		if !step.Done {
			complete = false
			break
		}
	}
	return model.OnboardingGuide{Steps: steps, Complete: complete, SampleModel: c.SampleModel}
}
