# Tenant Quota Semantics

Tenant quota fields are configured on `gateway_tenant`:

- `daily_token_limit`
- `monthly_token_limit`
- `monthly_request_limit`
- `monthly_budget_micros`

A value of 0 means unlimited.

## Enforcement

Monthly request quota and daily/monthly token quota use shared Redis keys, so multiple Gateway instances enforce the same counters.

Token flow:

```text
estimate
  -> reserve tenant daily/monthly quota
  -> reserve API-key TPM
  -> request
  -> actual usage
  -> reconcile both reservations
```

If trustworthy usage is absent, the estimate remains charged. Reservations are refunded when the request is rejected before reaching the upstream because of a later Gateway limiter.

Quota buckets currently use UTC day/month boundaries. If the production policy requires Asia/Shanghai billing boundaries, make that an explicit deployment policy before final acceptance.

## Budget

`monthly_budget_micros` is stored and exposed for budgeting/reporting, but monetary enforcement requires authoritative per-provider/model pricing plus actual route telemetry. It is intentionally not enforced from guessed prices.
