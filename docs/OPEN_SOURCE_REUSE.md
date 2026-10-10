# Open Source Reuse Policy

> Status: Active  
> Applies to: all new gateway development  
> Principle: **reuse mature open-source capabilities before writing new infrastructure code**

## 1. Required reuse check before coding

Before starting any feature, answer these questions in the task/PR:

1. Does ThinkWatch already provide the capability?
2. Does New API already provide a mature implementation or data model?
3. Does Maskit already provide a reusable security/proxy implementation?
4. Is there a mature Go ecosystem library for this capability?
5. What is the upstream license?
6. Can we depend on or call the upstream component instead of copying source?
7. If source must be copied, what is the smallest possible scope and what notices/obligations apply?
8. Why is custom implementation still necessary?

If a mature compatible implementation exists, new custom infrastructure code requires an explicit reason.

## 2. Reuse priority

Use this order:

1. **Call an existing service / API / process**
2. **Add an upstream library dependency**
3. **Use an adapter around an upstream implementation**
4. **Copy a small licensed module with required notices**
5. **Reimplement based on behavior/design**
6. **Build from scratch**

Prefer loose coupling over deep forks.

## 3. Current upstream decisions

| Project | License observed | Role in Campus AI Gateway | Reuse decision |
| --- | --- | --- | --- |
| ThinkWatch Core | MIT | routing, failover, circuit breaker, guard/data plane | **Direct reuse preferred**; keep it as the routing engine and integrate by API/config. Source reuse is possible when needed, while preserving MIT notices. |
| ThinkWatch Lite | MIT | ThinkWatch observability/admin tooling | **Direct reuse allowed**; prefer running alongside our admin UI rather than duplicating its low-level ThinkWatch views. |
| New API | AGPL-3.0 | channel/model management, priority/weight, model mapping, usage/quota, admin UX | **Do not copy source into this repository by default.** Reuse concepts/API behavior; consider it as a separate service only if needed. Direct source reuse requires an explicit licensing decision. |
| Maskit | AGPL-3.0 | privacy masking, proxy security, sensitive-data audit | **Do not copy source into this repository by default.** Prefer separate-service integration or design reference unless the project intentionally accepts AGPL obligations. |

Observed upstream repositories:

- ThinkWatch Core: https://github.com/ThinkWatchProject/ThinkWatch-Core
- ThinkWatch Lite: https://github.com/ThinkWatchProject/ThinkWatch-Lite
- New API: https://github.com/QuantumNous/new-api
- Maskit: https://github.com/xiaYuTian11/maskit

> This document is an engineering reuse policy, not legal advice. Before shipping copied third-party source, confirm license obligations with the organization responsible for delivery.

## 4. Important licensing rule

The Campus AI Gateway repository currently has no root license file recorded in the repository.

Therefore:

- Do not silently import copyleft source.
- MIT/BSD/Apache-style source may still require preserving copyright/license notices.
- AGPL/GPL-family source must not be copied into the gateway merely because it is technically convenient.
- If the team intentionally wants to incorporate AGPL code, make that a project-level licensing decision first.
- Running an upstream project as a separate service/process can be architecturally preferable to source copying, but its own license and deployment obligations still apply.

## 5. Reuse map by capability

| Capability | Preferred upstream | Action |
| --- | --- | --- |
| Provider routing | ThinkWatch Core | reuse |
| Failover | ThinkWatch Core | reuse |
| Circuit breaker | ThinkWatch Core | reuse |
| Guard / routing security primitives | ThinkWatch Core | reuse |
| ThinkWatch traces / low-level route visibility | ThinkWatch Lite | reuse where possible |
| Channel priority / weight concepts | New API | reference behavior/data model, do not copy AGPL code by default |
| Model mapping UX/semantics | New API | reference |
| Quota / usage product behavior | New API | reference; use our own tenant accounting unless a clean integration is selected |
| Sensitive-data masking | Maskit | prefer optional external integration; no source copy by default |
| Prometheus metrics | Prometheus Go ecosystem | use library, do not implement metric format manually |
| Distributed tracing | OpenTelemetry | use library |
| RBAC | mature Go RBAC library / campus IAM | use library/integration before custom authorization engine |
| Load testing | existing load-test tools | use tools, do not build a benchmark engine |

## 6. M1.5 reuse decisions

### M1.5-01 Model Route schema

Implemented locally because it is a campus control-plane schema that links our existing:

- gateway_model
- gateway_provider
- tenant-facing virtual model design
- ThinkWatch config renderer

New API's priority/weight/channel concepts were useful references, but its source is AGPL and its channel schema is not a drop-in match for our PostgreSQL/pgx control plane.

### M1.5-02 Model Route CRUD API

Before implementation:

- Reuse current Campus Gateway admin handler/storage conventions.
- Reuse the M1.5-01 gateway_model_route schema.
- Reuse ThinkWatch for actual route execution; **do not implement another routing engine in Go**.
- Use New API only to validate behavior such as priority/weight semantics and management UX.
- Do not copy New API CRUD/controller/model source into the repository by default because it is AGPL and also tied to a different application architecture.

The CRUD layer remains small campus-specific control-plane glue.

### M1.5-03 Admin Route UI

Reuse the current embedded admin UI first.

Before building new components, review New API's channel-management UX for:

- priority editing
- weight editing
- enabled/disabled status
- model mapping presentation
- test actions

Reuse interaction ideas, not AGPL source, unless licensing is intentionally changed.

### M1.5-05 / M1.5-06 Routing and Failover

Do **not** implement custom failover logic in Campus Gateway unless ThinkWatch cannot satisfy a documented requirement.

Target:

```text
Campus control plane
  -> renders/synchronizes route policy
  -> ThinkWatch Core
  -> performs routing/failover/circuit-breaking
```

## 7. PR requirement

Every non-trivial feature PR must contain a short section:

```text
Open Source Reuse Check
- ThinkWatch:
- New API:
- Maskit:
- Other libraries:
- Directly reused:
- Not reused because:
- License notes:
```

A review should reject unnecessary infrastructure reimplementation when a stable compatible upstream capability already exists.

## 8. Architecture rule

The gateway should own **campus policy**, not generic infrastructure that a mature component already provides.

Campus-owned examples:

- Tenant
- Virtual API Key
- school SSO integration
- campus model permissions
- campus quota policy
- campus audit requirements
- ThinkWatch control-plane synchronization

Prefer upstream reuse for:

- routing engines
- circuit breakers
- telemetry standards
- authentication primitives
- generic RBAC engines
- metrics/tracing formats
- load-test engines

## 9. Decision log

Add future reuse decisions here before implementation.

| Date | Feature | Upstream evaluated | Decision |
| --- | --- | --- | --- |
| 2026-10-10 | Multi-provider routing | ThinkWatch Core, New API | ThinkWatch executes routing/failover; Campus Gateway owns control-plane Route CRUD; New API used as design reference only because of AGPL and architecture mismatch. |
| 2026-10-10 | Privacy/security proxy | Maskit | Keep as optional future external integration/reference; no source import by default because of AGPL. |
