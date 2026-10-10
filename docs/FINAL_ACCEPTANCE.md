# Final Acceptance Checklist

This checklist is intentionally the point where the project owner performs the consolidated manual/environment validation. Development-time CI and static checks happen before this stage.

## 1. Update and start

```powershell
cd D:\AI\campus-ai-gateway
git switch main
git pull
.\scripts\dev.ps1 -ForceRestart
```

Expected:

- PostgreSQL ready
- Redis ready
- Campus Gateway ready
- ThinkWatch Core ready
- Admin UI opens
- migrations 0001 through 0006 replay without error

## 2. Baseline Qwen regression

Using an existing `sk-campus-*` key authorized for `campus-smart`:

- GET `/v1/models` returns the virtual alias only
- non-streaming chat returns HTTP 200 and usage
- SSE chat streams without buffering
- request log contains request ID, tokens, latency and status
- RPM / TPM / concurrency / model / IP denials still behave as expected

## 3. Multi-provider route setup

In Admin UI:

1. Add/confirm Qwen Provider.
2. Use “添加 DeepSeek” and ensure `DEEPSEEK_API_KEY` is present in the ThinkWatch process environment.
3. Open `campus-smart -> 管理线路`.
4. Configure:
   - Qwen priority 1
   - DeepSeek `deepseek-chat` priority 2
5. Synchronize to ThinkWatch.
6. Run model test.

## 4. Real failover

Set a valid business key for the test:

```powershell
$env:ADMIN_TOKEN = "<admin-token>"
$env:CAMPUS_API_KEY = "sk-campus-..."
.\scripts\verify-failover.ps1 -Execute
```

Expected:

```text
[PASS] Primary was unreachable and the virtual-model request still succeeded.
[OK] Primary provider restored.
```

If this passes, M1.5 real Qwen -> DeepSeek failover can be marked environment-verified.

## 5. Route audit

Confirm request logs expose:

- requested_model
- actual_model
- provider
- upstream_model
- route_priority
- attempts
- failover
- tokens / latency / status

Important: the current ThinkWatch Core version inspected does not expose final route facts as normal HTTP response headers. Provider/attempt fields are populated only when a compatible telemetry bridge/header contract is present. Empty provider/attempt fields are not a Gateway failure and must not be replaced by guessed values.

## 6. Security and admin audit

Trigger:

- invalid API key
- expired/disabled key if available
- model forbidden
- IP forbidden
- RPM / TPM / concurrency denial
- tenant request/token quota denial

Check “安全事件”.

Perform Provider/Model/Route/Tenant/API Key changes and ThinkWatch sync. Check “管理审计”.

## 7. SSO / RBAC

Development mode:

```text
ADMIN_AUTH_MODE=token
```

Production acceptance should run behind the university OIDC reverse-auth layer:

```text
Campus IdP -> oauth2-proxy -> Gateway trusted-header RBAC
```

Verify each role:

- super_admin
- gateway_admin
- tenant_admin
- auditor
- operator

Also verify direct access that bypasses the trusted proxy cannot forge admin identity.

## 8. Quota accounting

Configure a Tenant with small limits and verify:

- monthly request quota returns 429 at the limit
- daily/monthly token reservation returns 429 when exhausted
- successful responses with usage reconcile estimates to actual token count
- Admin UI “用量” shows daily/monthly aggregate usage

Monetary budget is reporting configuration only until authoritative provider pricing and actual route telemetry are available.

## 9. Admin productization

Verify:

- route CRUD UI
- dashboard P95 / failover / provider metrics
- request-log filtering
- pagination
- CSV export
- tenant usage
- admin audit
- security events

## 10. Embedding / ASR foundation

If the selected ThinkWatch/provider adapters support the target service:

- POST `/v1/embeddings` using a virtual model
- POST `/v1/audio/transcriptions` multipart with a virtual model

The Gateway supports authentication/model authorization and transparent forwarding. Provider-specific real calls are separate final acceptance items.

## 11. Production health and monitoring

Check:

- `/healthz`
- `/readyz`
- `/metrics`
- Prometheus scrape
- HTTPS edge config
- secrets supplied externally
- backup/restore runbook
- multi-Gateway readiness behavior

## 12. 30,000 RPM / 500 RPS

Follow `docs/PERFORMANCE_TEST.md`.

At minimum run:

```powershell
k6 run .\loadtest\k6-models.js
k6 run .\loadtest\k6-chat.js
```

with `TARGET_RPS=500` in a representative environment.

Do not mark 30,000 RPM as passed until the benchmark report records achieved RPS, P50/P95/P99, error rate, CPU, memory, Redis, PostgreSQL, Gateway and ThinkWatch resource data.

## Acceptance summary

Items that require real environment acceptance and cannot be truthfully completed by repository CI alone:

- DeepSeek credential / real call
- Qwen -> DeepSeek real failover
- ThinkWatch actual-route telemetry integration
- university IdP SSO
- production HA/failover
- real embedding / ASR provider calls
- 30,000 RPM benchmark
