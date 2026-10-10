# Performance Acceptance

The procurement target is **30,000 RPM ~= 500 requests/second**. The repository contains repeatable k6 scenarios, but this number must not be marked as passed until a representative deployment produces a stored benchmark result.

## Required environment

Use the same topology intended for production:

- load balancer / reverse proxy
- at least the intended number of Gateway instances
- production-class PostgreSQL and Redis
- ThinkWatch Core
- real or controlled upstream model endpoints
- monitoring enabled

Do not benchmark a laptop loopback setup and call it production capacity.

## Scenarios

### Model authorization path

```powershell
$env:BASE_URL = "https://gateway.example.edu"
$env:API_KEY = "sk-campus-..."
$env:TARGET_RPS = "500"
$env:DURATION = "2m"
k6 run .\loadtest\k6-models.js
```

### Non-streaming chat path

```powershell
$env:BASE_URL = "https://gateway.example.edu"
$env:API_KEY = "sk-campus-..."
$env:MODEL = "campus-smart"
$env:TARGET_RPS = "500"
$env:DURATION = "2m"
k6 run .\loadtest\k6-chat.js
```

Run separate tests for SSE, rate-limit bursts and the Qwen -> DeepSeek failover path. Provider-side quotas make uncontrolled 500 RPS generation tests expensive, so use a controlled upstream when validating Gateway capacity, then run a smaller real-provider acceptance test separately.

## Record

For each run store:

- git commit SHA
- topology / instance count
- target and achieved RPS
- P50 / P95 / P99
- error rate
- CPU and memory
- Go goroutines
- PostgreSQL connections / query latency
- Redis ops / latency
- ThinkWatch resource use
- upstream mode (real vs controlled)
- streaming vs non-streaming
- failover enabled/disabled

## Pass criteria

The 30,000 RPM requirement is accepted only if a repeatable run sustains at least 500 RPS at the Gateway boundary with an agreed error-rate target and without violating the response-time requirement.

The supplied k6 threshold is a useful guardrail, not proof by itself.

## Current status

**Tooling ready; production benchmark pending final environment acceptance.**
