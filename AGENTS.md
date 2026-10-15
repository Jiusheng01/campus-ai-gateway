# Campus AI Gateway - Codex instructions

This file guides AI coding agents operating inside this repository. Follow it together with the actual user task. Keep the process proportional to the task.

## Project and source of truth
- Go 1.24 backend in `cmd/` and `internal/`; React/TypeScript/Vite admin console in `web/admin/`.
- Gateway owns tenant/API-key access, model/IP permissions, quotas, audit, RBAC and admin session security; ThinkWatch Core owns upstream routing/failover. Do not blur that boundary.
- Read `README.md` and the relevant code before designing changes. Consult `docs/DEVELOPMENT_PLAN.md` for scope and `docs/FINAL_ACCEPTANCE.md` for environment-dependent acceptance; both can lag actual code.
- Check `git status`, active branch and open PR overlap before editing. Work on a feature/chore branch, never directly on `main`.

## Proportionate Codex workflow
- Small fix: reproduce/inspect -> minimal change -> focused test -> review.
- Regular feature: clarify behavior and acceptance -> implement -> focused tests -> review.
- Cross-module/risky change: `think` -> `grill-me` or `grill-with-docs` -> spec/tickets when useful -> `implement` -> tests -> independent review/`check`.
- Matt Pocock, Waza and Ponytail skills are optional **local Codex plugins**, not repository dependencies. Detect available skills; do not fail or install plugins automatically if missing. Use ordinary reasoning and commands as fallback.
- Apply first principles and adversarial review to important decisions. Use controlled ablation only for measurable optimizations. Favor the simplest correct design; do not weaken safety to reduce code size.

## Behavioral invariants
- Preserve OpenAI-compatible `/v1/*` API, `campus-smart` virtual model routing and existing tenant/API-key controls unless explicitly scoped otherwise.
- Preserve streaming/SSE semantics, rate/quota accounting and accurate request/audit facts. Never invent upstream provider/attempt telemetry when not supplied by ThinkWatch.
- Treat authentication/registration/role changes as security-sensitive. Prove negative authorization cases and session revocation. Do not casually alter login modes or last-super-admin protections.
- Schema changes are additive migrations; do not rewrite applied `migrations/*.sql` files.
- Do not check in secrets, live `.env`, API keys, login cookies or ThinkWatch credentials. Mask values in logs and handoffs.
- Prefer the current module structure and dependencies. Avoid unrelated refactors and speculative frameworks.

## Data and environment safety: mandatory
- Before running any script, inspect it for DB writes, Redis deletes, service restart, provider charges, or edits to external config.
- `scripts/e2e-admin-auth-v2.sh` **deletes rows in `gateway_admin_user`** and removes Redis `gw:admin:*` keys. NEVER run it against an existing personal/shared/staging/production DB or cache. Use only an explicitly verified disposable isolated PostgreSQL+Redis pair and obtain the owner's approval for stateful E2E.
- `scripts/dev.ps1` starts services and applies migrations; `scripts/verify-failover.ps1 -Execute` can change runtime routing/configuration. Treat these as stateful actions requiring review of the target environment.
- Never reset/drop/truncate shared data; rotate credentials; run real provider requests or load tests; modify `%APPDATA%\ThinkWatch\config.yaml`; or deploy/reload services without explicit authorization.
- Prefer read-only inspection, unit tests and CI on the first workflow trial. Report a skipped test as SKIPPED, not PASS.

## Verification
- Backend: `go test -count=1 ./...`, `go vet ./...`, `go build ./...`.
- Frontend: `npm --prefix web/admin run check`, `npm --prefix web/admin run build` (install dependencies first when required).
- CI: `.github/workflows/ci.yml` checks/builds the frontend, then runs Go tests and Go build on PRs.
- Add focused regression tests for behavior changes, especially auth, RBAC, quotas, streaming and routing. Use disposable fixtures and bounded timeouts.
- Real provider failover, campus IdP, HA, benchmark and model telemetry need real-environment evidence; successful unit tests or CI do NOT prove these.

## Delivery
- Before changes: summarize goal, in/out of scope, affected modules, risks and acceptance criteria. For simple changes this can be brief.
- After changes: report changed files, test commands with actual PASS/FAIL/SKIPPED, remaining risks and how to reproduce. Separate observed facts from assumptions.
- Keep one PR focused on one theme. Creating a PR is not permission to merge it; do not auto-merge or force-push without explicit instruction.
- For cross-session handoff use the `handoff` skill if present; otherwise write a concise summary containing base/ref, exact changes, commands, test results and remaining work.
