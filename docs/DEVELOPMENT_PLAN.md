# Campus AI Gateway Development Plan

> Version: v1.0  
> Status: Active  
> Baseline: M1 completed and verified  
> Rule: Unless an architecture-level issue is discovered, follow this plan in order.

## 1. Project Scope

This repository focuses on the campus AI platform's model infrastructure layer:

- unified LLM access
- model routing
- tenant and Virtual API Key management
- rate limiting and quota control
- security governance
- request audit
- model runtime observability

This repository does **not** attempt to rebuild the complete campus AI portal, knowledge base, agent platform, customer service system, or workflow platform.

## 2. Stable Baseline

The following capabilities are treated as the current stable baseline:

- Go Gateway
- PostgreSQL
- Redis
- ThinkWatch Core
- Qwen provider
- OpenAI-compatible API
- `campus-smart` virtual model
- Tenant management
- Virtual API Key
- Model allowlist
- IP allowlist
- RPM limiting
- TPM basic control
- Concurrency limiting
- SSE proxy
- Request ID
- Token usage collection
- Request audit
- Provider management
- Model management
- Embedded Admin UI
- ThinkWatch config sync
- Web model test
- Windows setup/dev/status/stop scripts

Verified end-to-end path:

```text
Tenant
  -> sk-campus-* Virtual Key
  -> Go Gateway
  -> Model/IP authorization
  -> RPM/TPM/Concurrency
  -> ThinkWatch Core
  -> campus-smart
  -> Qwen
  -> OpenAI-compatible response
  -> Token/latency/status audit
  -> PostgreSQL
```

The baseline should not be broken by later feature work.

## 2.1 Implementation Status - 2026-10-10

The planned repository-side work has progressed through M4 foundations.

| Area | Repository implementation | Final environment acceptance |
| --- | --- | --- |
| M1.5 Route schema / CRUD / Admin UI | Complete | Qwen baseline already verified |
| M1.5 DeepSeek / multi-provider renderer | Complete | Real DeepSeek credential required |
| M1.5 Qwen -> DeepSeek failover | Verification script complete | Real dual-provider run pending |
| M1.6 Route audit | Schema/capture contract complete | Final provider/attempt telemetry bridge pending |
| M2.0 Security/Admin audit | Complete | Final combined regression pending |
| M2.1 SSO/RBAC | Gateway trusted-header/RBAC complete | University IdP integration pending |
| M2.2 TPM/Tenant quota | Reserve/reconcile + Redis tenant enforcement complete | Final quota scenarios pending |
| M2.3 Admin UI productization | Complete for planned current scope | Browser regression pending |
| M3.0 Performance/prod tooling | k6/health/readiness/HTTPS guidance complete | 30,000 RPM benchmark pending |
| M3.1 HA/monitoring | Metrics/Prometheus/HA guidance complete | Infrastructure failover pending |
| M4.0 Model-service foundation | Embedding + multipart ASR gateway foundation complete | Real provider calls pending |

“Complete” in the middle column means code/tooling is present in the repository and CI-covered where applicable. It does **not** convert external/provider/infrastructure acceptance into a claimed pass.

## 3. Milestones

| Milestone | Goal | Priority |
| --- | --- | --- |
| M1.5 | Multi-provider model routing and failover | P0 |
| M1.6 | Route audit and observability | P0 |
| M2.0 | Security events and admin audit | P0 |
| M2.1 | SSO and RBAC | P0 |
| M2.2 | Accurate quota and token accounting | P1 |
| M2.3 | Admin UI productization | P1 |
| M3.0 | Performance validation and production hardening | P0 |
| M3.1 | High availability and monitoring | P1 |
| M4.0 | Embedding / ASR / additional model services | P1 |

## 4. M1.5 - Multi-provider Routing and Failover

### 4.1 Data model

Current:

```text
gateway_model
  alias
  provider_id
  upstream_model
```

Target:

```text
gateway_model
  id
  alias
  status

gateway_model_route
  id
  model_id
  provider_id
  upstream_model
  priority
  weight
  timeout_ms
  status
```

Example:

```text
campus-smart
  P1 -> qwen      -> qwen3.7-flash-...
  P2 -> deepseek  -> deepseek-chat
  P3 -> glm       -> glm-...
```

### 4.2 Migration

Create:

```text
migrations/0003_model_routes.sql
```

Rules:

- Do not edit already-released migrations.
- Convert existing model/provider binding into the first route automatically.
- Preserve the current Qwen path without downtime.

### 4.3 Provider onboarding order

1. Qwen - completed
2. DeepSeek
3. GLM
4. Kimi
5. Doubao

Do not add all providers at once. Complete real failover validation with Qwen + DeepSeek first.

### 4.4 M1.5 Definition of Done

Normal path:

```text
campus-smart
  -> Qwen
  -> 200
```

Failure path:

```text
campus-smart
  -> Qwen
  -> failure
  -> DeepSeek
  -> 200
```

M1.5 is not complete until both paths are verified with real requests.

## 5. M1.6 - Route Audit and Observability

Extend request audit to include:

- requested_model
- provider
- upstream_model
- route_priority
- attempts
- failover
- input_tokens
- output_tokens
- total_tokens
- latency_ms
- status_code
- error_code

Create:

```text
migrations/0004_route_audit.sql
```

Expected log example:

```text
requested_model = campus-smart
provider        = deepseek
upstream_model  = deepseek-chat
route_priority  = 2
attempts        = 2
failover        = true
status          = 200
```

## 6. M2.0 - Security Events and Admin Audit

Keep business request logs and admin operation logs separate.

Add:

```text
gateway_admin_audit_log
gateway_security_event
```

Security event types:

- invalid_api_key
- model_forbidden
- ip_forbidden
- rpm_exceeded
- tpm_exceeded
- concurrency_exceeded
- provider_failure
- circuit_open
- guard_blocked

Admin audit examples:

- provider created
- route priority changed
- model disabled
- tenant disabled
- API key disabled
- ThinkWatch config synchronized

## 7. M2.1 - SSO and RBAC

The current `ADMIN_TOKEN + localStorage` design is development-only.

Target:

```text
Campus SSO
  -> Admin Login
  -> Session
  -> User
  -> Role
  -> Permission
```

Initial roles:

| Role | Scope |
| --- | --- |
| super_admin | All permissions |
| gateway_admin | Provider / Model / Route |
| tenant_admin | Own tenant |
| auditor | Read-only audit |
| operator | Runtime status / model test |

## 8. M2.2 - Accurate Quota Accounting

Current TPM is request-side reservation.

Target flow:

```text
request
  -> estimate tokens
  -> reserve
  -> model request
  -> receive actual usage
  -> reconcile
  -> Redis correction
```

Add tenant-level statistics:

- daily requests
- monthly requests
- daily tokens
- monthly tokens
- budget/quota
- estimated cost
- current RPM
- error rate

## 9. M2.3 - Admin UI Productization

Do not rewrite the UI before backend behavior is stable.

Planned dashboard metrics:

- request trend
- token trend
- provider success rate
- P50/P95/P99 latency
- failover count
- error rate
- tenant Top N

Request log filters:

- time range
- tenant
- provider
- model
- HTTP status
- error code
- failover
- request ID
- pagination
- detail view
- export

## 10. M3.0 - Performance Validation

Target requirement:

```text
30,000 RPM ~= 500 requests/second
```

Must test separately:

- /v1/models
- chat non-streaming
- SSE
- Virtual Key auth
- RPM
- TPM
- concurrency
- PostgreSQL audit
- provider normal path
- provider failover
- burst traffic

Record:

- RPS
- P50
- P95
- P99
- error rate
- CPU
- memory
- Redis ops
- DB connections
- goroutines

Do not mark the 30,000 RPM requirement complete before a repeatable benchmark is recorded.

## 11. M3.1 - Production Architecture

Target shape:

```text
Load Balancer
  -> Gateway 1
  -> Gateway 2
  -> ThinkWatch
  -> Providers

Gateway -> Redis HA/Cluster
Gateway -> PostgreSQL HA
```

Production tasks:

- HTTPS
- secret management
- log rotation
- metrics
- Prometheus
- Grafana
- alerts
- backup/restore
- health checks
- graceful shutdown
- PostgreSQL HA
- Redis HA

## 12. Team Workstreams

Recommended ownership:

| Workstream | Area |
| --- | --- |
| A | Go Gateway / Control Plane |
| B | ThinkWatch / Routing / Failover |
| C | Admin UI |
| D | PostgreSQL / Redis / Audit |
| E | QA / Security / Performance |

Example M1.5 parallelization:

- A: `0003_model_routes.sql` + Model Route API
- B: ThinkWatch route renderer + failover validation
- C: Route management UI
- D: Route audit schema/design
- E: regression and failure-path tests

## 13. Git Collaboration Rules

Do not develop directly on `main`.

Branch naming:

```text
feature/model-routes
feature/deepseek-provider
feature/route-audit
feature/admin-route-ui

fix/thinkwatch-reload
fix/token-reconcile

refactor/proxy
docs/development-plan
```

Flow:

```text
main
  -> feature/*
  -> Pull Request
  -> Review
  -> go test ./...
  -> Merge
```

Guideline:

```text
One PR = one coherent feature
```

Avoid combining unrelated database, UI, authentication, routing, and scripting changes in a single PR.

## 14. Database Migration Rules

Migration sequence:

```text
0001_init.sql
0002_management.sql
0003_model_routes.sql
0004_route_audit.sql
0005_security_events.sql
...
```

Rules:

- only append migrations
- never rewrite a migration already executed by teammates/environments
- migrations must be idempotent where practical
- every schema change must include upgrade verification

## 15. API Compatibility Principle

External business clients should normally use virtual model names only:

```text
campus-smart
```

Do not bind business applications directly to:

```text
qwen3.7-...
deepseek-chat
glm-...
```

Target design:

```text
Business App
  -> campus-smart
  -> Gateway policy
  -> Qwen / DeepSeek / GLM / Kimi
```

This preserves transparent model switching and failover.

## 16. Definition of Done

A feature is not done when coding stops.

Required:

```text
implementation
  -> migration
  -> go test ./...
  -> API test
  -> Admin UI verification
  -> failure-path test
  -> docs update
  -> PR review
  -> merge
```

For provider/model features:

- real provider request must pass

For routing features:

- normal route must pass
- failure/failover route must pass

For performance work:

- repeatable benchmark report must be stored

## 17. Fixed Development Order

Follow this order unless an architecture issue requires re-planning:

```text
M1.5-01  0003_model_routes.sql
M1.5-02  Model Route CRUD API
M1.5-03  Admin Route UI
M1.5-04  DeepSeek Provider
M1.5-05  ThinkWatch multi-provider renderer
M1.5-06  Qwen -> DeepSeek failover verification

M1.6     Actual Provider / Upstream audit

M2.0     Security events + admin audit

M2.1     SSO + RBAC

M2.2     TPM/quota reconciliation

M3       30,000 RPM benchmark + production hardening
```

## 18. Next Task

The next task is no longer another foundation feature. It is the consolidated final-environment acceptance:

```text
docs/FINAL_ACCEPTANCE.md
```

Priority acceptance items:

1. DeepSeek real request.
2. Qwen -> DeepSeek real failover.
3. ThinkWatch actual route telemetry bridge/verification.
4. University SSO + RBAC.
5. Tenant quota regression.
6. Embedding / ASR real provider validation where required.
7. Multi-instance / PostgreSQL / Redis HA exercise.
8. 30,000 RPM ~= 500 RPS repeatable benchmark report.

Do not mark environment-dependent requirements complete until those tests produce evidence.
