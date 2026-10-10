# Production Deployment and HA

## Edge

Terminate HTTPS at a managed load balancer or Nginx. Keep SSE buffering disabled and use long upstream read timeouts. Do not expose PostgreSQL, Redis, ThinkWatch control ports or Gateway admin ports directly to the public Internet.

## Gateway

Run at least two stateless Gateway instances behind the load balancer. Shared state belongs in PostgreSQL and Redis.

Endpoints:

- `/healthz` - process liveness
- `/readyz` - PostgreSQL + Redis readiness
- `/metrics` - Prometheus text exposition for Gateway business metrics

Graceful SIGINT/SIGTERM shutdown is enabled with a 10-second drain window. Streaming workloads may require a larger orchestration termination grace period than the process shutdown timeout.

## Secrets

Production values must come from the deployment secret mechanism, not committed files:

- `API_KEY_HMAC_SECRET`
- `ADMIN_PROXY_SECRET`
- PostgreSQL / Redis credentials
- `THINKWATCH_GATEWAY_KEY`
- `DASHSCOPE_API_KEY`
- `DEEPSEEK_API_KEY`
- campus OIDC client secret (owned by the reverse-auth component)

Kubernetes Secret, Vault or the organization's KMS/secret manager are preferred.

## PostgreSQL

Recommended production baseline:

- managed PostgreSQL or primary + standby
- automated backups + point-in-time recovery
- connection pooling
- TLS in transit
- tested restore procedure
- monitor connection saturation and slow queries

## Redis

Recommended production baseline:

- managed HA Redis / Sentinel / cluster according to organizational standard
- authentication and TLS
- no public exposure
- persistence policy chosen for rate/quota semantics
- monitor memory, evictions, latency and reconnects

## ThinkWatch

ThinkWatch Core remains the runtime routing/failover/circuit-breaker engine. Keep its control interface on a management network. Campus Gateway sync writes must continue to pass `twcore check` before replacement.

## Monitoring

Prometheus can scrape `/metrics`. A starter configuration is in `deploy/monitoring/prometheus.yml`.

Alert at minimum on:

- readiness failure
- elevated HTTP error rate
- P95/P99 latency
- failover spikes
- Redis/PostgreSQL unavailability
- provider health/circuit-open signals from ThinkWatch
- disk/backup failures

## HA status

The repository contains the stateless Gateway design, health/readiness endpoints and deployment guidance. Actual PostgreSQL/Redis/load-balancer failover remains an infrastructure acceptance test and must not be claimed as verified until tested in the target environment.
