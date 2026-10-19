# Campus AI Gateway

**English** · [简体中文](README.zh-CN.md)

[![ci](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

One OpenAI-compatible endpoint in front of many model providers, with tenants, API keys, quota, rate limits, routing with failover, a circuit breaker and an audit trail — plus an admin console. A single Go process with PostgreSQL and Redis; no sidecar, no second data plane.

> This is not a chatbot, an agent framework or a knowledge base. It is the layer underneath one: **unified model access + access control + routing + quota + audit + an admin control plane.**

---

## Why this exists

- **One API, many providers.** Callers keep speaking `/v1/chat/completions`. The gateway resolves the virtual model to a provider, injects that channel's credential and streams the answer back untouched.
- **The Tenant is the boundary.** Quota, rate limits (RPM / TPM / concurrency) and audit are defined on the Tenant, so a department has one budget rather than one per API key.
- **Routing that survives a bad channel.** Priority bands, smooth weighted round-robin inside a band, a per-provider circuit breaker that skips a failing channel, and a recorded attempt chain for every request.
- **Built to be audited.** Business request logs, admin audit and security events are separate records with separate durability guarantees, and no upstream response body is ever written. Keeping them apart is deliberate; the [design page](doc/design.md) says which is which.

---

## Architecture

```text
Clients / AI apps
        │
        │  OpenAI-compatible API
        │  Authorization: Bearer sk-campus-...
        ▼
┌──────────────────────────────────────────────┐
│              Campus AI Gateway               │
│                                              │
│ Tenant / API Key / Model & IP allowlist      │
│ RPM / TPM / concurrency / quota              │
│ Request ID / usage / audit                   │
│                                              │
│ Admin session auth / RBAC / admin audit      │
│ Embedded admin UI                            │
│                                              │
│ Direct data plane (in-process)               │
│ Routing · Failover · Circuit breaker         │
└────────────────────┬─────────────────────────┘
                     │
          ┌──────────┴──────────┐
          ▼                     ▼
   Alibaba Cloud Qwen     Volcengine Ark
        / ...                  / ...

Data:
  PostgreSQL -> tenants / keys / providers / models / sessions / audit
  Redis      -> RPM / TPM / concurrency / login throttle
```

There is exactly one data plane, implemented in-process: the gateway resolves the virtual model against the routes you configured, injects the channel credential, streams the response, and fails over by priority. It needs PostgreSQL and Redis and nothing else.

---

## Capabilities

| Capability | Status | Notes |
| --- | --- | --- |
| OpenAI-compatible API | ✅ | `/v1/models`, `/v1/chat/completions`; embeddings / ASR proxy groundwork kept |
| Business API keys | ✅ | `sk-campus-*`; only a prefix and an HMAC-SHA256 digest are stored |
| Tenant management | ✅ | create, edit, enable / disable, quota and usage |
| Model / IP allowlist | ✅ | per business key |
| RPM / TPM / max concurrency | ✅ | Redis; token reservation reconciled against the final usage |
| Direct data plane | ✅ | the only data plane: priority failover, weighted routing, circuit breaker |
| Weighted routing | ✅ | smooth weighted round-robin inside a `Route Priority` band |
| Circuit breaker | ✅ | per provider, closed / open / half-open, instance-local |
| Per-attempt telemetry | ✅ | every routing attempt is recorded, and the console shows the chain for a request |
| Provider / virtual model / route control plane | ✅ | one virtual model can bind many provider routes |
| Request ID / request audit | ✅ | every business request recorded: model, channel, tokens, latency, outcome |
| SSE transparent proxy | ✅ | streaming responses are not buffered |
| Admin UI | ✅ | React + TypeScript + Vite, built output embedded into the Go binary |
| Admin session auth v2 | ✅ | first-run setup, password login, access + refresh, device sessions |
| Unified accounts & registration | ✅ | public sign-up creates a `member`; only a super admin grants admin roles |
| RBAC | ✅ | `super_admin / gateway_admin / tenant_admin / auditor / operator` |
| trusted-header / campus SSO boundary | ✅ | gateway side complete; a real campus IdP is still to be accepted |
| Admin audit / security events | ✅ | kept separate from business request logs |
| Prometheus / health checks | ✅ | `/healthz`, `/readyz`, `/metrics` |
| 30,000 RPM | 🧊 frozen | k6 tooling exists; the performance target awaits a representative environment |

---

## Quick start

Requirements: **Go 1.24+**, **Docker Desktop**, **Node.js 22+** (for building the console).

Windows (PowerShell):

```powershell
git clone https://github.com/Jiusheng01/campus-ai-gateway.git
cd campus-ai-gateway
.\scripts\setup.ps1          # first run: starts PostgreSQL / Redis and initialises
.\scripts\dev.ps1            # day to day
.\scripts\status.ps1         # what is running
.\scripts\stop.ps1           # stop
```

Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Then open the console at <http://127.0.0.1:8080/admin-ui> and create the first administrator there. **There is no built-in account or default password.**

> The gateway takes all of its configuration from **process environment variables** and never reads a `.env` file. [`.env.example`](.env.example) is a reference list of variable names. See [Configuration & credentials](doc/configuration.md).

---

## Call the API

Create a tenant and a business key in the console, set the key's model allowlist, then:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": false
  }'
```

A request passes through the tenant, key, allowlist, quota, rate-limit, routing and audit path before it reaches a provider.

---

## Documentation

Operator documentation is written in Chinese.

- **[Configuration & credentials](doc/configuration.md)** — environment variables, provider presets, credential rules, production secrets.
- **[Authentication & accounts](doc/authentication.md)** — first-run setup, sessions, registration, RBAC, trusted-header / campus SSO.
- **[Operations](doc/operations.md)** — local development, migrations, building the console, testing and acceptance.
- **[Design, limits and roadmap](doc/design.md)** — the data plane, the record model, known limitations and what is next.

---

## Status

The gateway is runnable and manageable. Qwen has been exercised end to end and Volcengine Ark connectivity is verified. Still open, and deliberately frozen for now: campus IdP acceptance, multi-gateway / PostgreSQL / Redis HA, embedding and ASR providers, and the 30,000 RPM target. See [limits and roadmap](doc/design.md).

---

## License

MIT — see [LICENSE](LICENSE).
