<div align="center">

# Campus AI Gateway

**One OpenAI-compatible endpoint in front of many model providers.**

<sub>Tenants · API keys · Quota · Rate limits · Routing · Failover · Circuit breaker · Audit · Admin console</sub>

[English](README.md) · [简体中文](README.zh-CN.md)

[![ci](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

</div>

---

A single Go process with PostgreSQL and Redis. Callers keep speaking the OpenAI API; the gateway resolves the model to a provider, injects that channel's credential, and streams the answer back untouched.

> Not a chatbot, an agent framework or a knowledge base. It is the layer underneath one: **unified model access + access control + routing + quota + audit + an admin control plane.**

## Capabilities

<table>
<tr>
<td width="50%" valign="top">

### 🔑 The Tenant is the boundary

Quota, rate limits (RPM / TPM / concurrency) and audit are defined on the Tenant, so a department has **one budget**, not one per API key. Every key under it shares the same ceiling.

</td>
<td width="50%" valign="top">

### 🔀 Routing that survives a bad channel

Priority bands, smooth weighted round-robin inside a band, a per-provider circuit breaker that skips a failing channel, and a recorded attempt chain for every request.

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 🔌 Add a provider, don't touch clients

One OpenAI-compatible surface for every provider. Register a channel in the console — paste the vendor API key (sealed and write-only) or reference an environment variable — and bind it to a virtual model; callers never learn it changed.

</td>
<td width="50%" valign="top">

### 🛡️ Fail-closed by default

An empty model allowlist denies everything; an inline channel key is sealed with AES-256-GCM and never read back; an environment-variable credential may not point at a gateway-owned variable; and enforcement state that cannot be read refuses the request rather than admitting it.

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 📊 Audit you can defend

Business request logs, admin audit and security events are separate record classes with separate durability, and no upstream response body is ever stored.

</td>
<td width="50%" valign="top">

### 🖥️ An admin console, included

A React console embedded in the Go binary: tenants, keys, providers, routes, usage, audit. Session auth with Argon2id, five RBAC roles, and a trusted-header path for campus SSO.

</td>
</tr>
</table>

## Architecture

```mermaid
flowchart TB
    Client["Clients / AI apps<br/>OpenAI-compatible API · Bearer sk-campus-…"] --> GW

    subgraph GW["Campus AI Gateway · one Go process"]
        direction TB
        Auth["Authentication & authorization<br/>Tenant · business API key · model &amp; IP allowlist"]
        Limit["Quota &amp; rate limits<br/>daily / monthly quota · RPM / TPM / concurrency"]
        Route["Routing<br/>priority bands · weighted round-robin · failover · circuit breaker"]
        Record["Records<br/>request log · usage · audit"]
        Auth --> Limit --> Route --> Record
    end

    Route --> Qwen["Alibaba Cloud Qwen"]
    Route --> Ark["Volcengine Ark"]
    Route --> More["…"]

    GW --- PG[("PostgreSQL")]
    GW --- Redis[("Redis")]
```

The data plane is in-process — there is no sidecar and no second gateway to synchronise with. PostgreSQL holds the control-plane state and the records; Redis holds the live counters (rate limits, quota projections, login throttling).

## Quick start

Requirements: **Go 1.24+**, **Docker Desktop**, **Node.js 22+** (to build the console).

Windows (PowerShell):

```powershell
git clone https://github.com/Jiusheng01/campus-ai-gateway.git
cd campus-ai-gateway
.\scripts\setup.ps1          # first run: PostgreSQL / Redis + initialise
.\scripts\dev.ps1            # day to day
.\scripts\status.ps1         # what is running
.\scripts\stop.ps1           # stop
```

Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Then open <http://127.0.0.1:8080/admin-ui> and create the first administrator there. **There is no built-in account or default password.**

The first hop stays in the console: add a channel, paste the vendor API key into the password field, and bind a virtual model to that channel. The key is sealed with AES-256-GCM on save and never returned — the console only shows that it is configured and its last four characters. If you would rather use an environment variable, switch the channel to "reference an environment variable" and enter its name instead.

> All configuration comes from **process environment variables**; no `.env` file is read. Provider API keys are the exception: paste them into the channel form and the gateway seals them in PostgreSQL. [`.env.example`](.env.example) is a reference list of names — see [Configuration](doc/configuration.md).

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

## Documentation

Operator documentation is written in Chinese.

| | |
| --- | --- |
| [**Configuration**](doc/configuration.md) | environment variables, provider presets, credential rules, production secrets |
| [**Authentication**](doc/authentication.md) | first-run setup, sessions, registration, RBAC, trusted-header / SSO |
| [**Operations**](doc/operations.md) | local development, migrations, console build, testing and acceptance |
| [**Design & roadmap**](doc/design.md) | the data plane, the record model, known limits and what is next |

## Status

Runnable and manageable today: Qwen has been exercised end to end and Volcengine Ark connectivity is verified. Deliberately frozen for now — campus IdP acceptance, multi-gateway / PostgreSQL / Redis HA, embedding and ASR providers, and the 30,000 RPM target. See [design & roadmap](doc/design.md).

## License

MIT — see [LICENSE](LICENSE).
  