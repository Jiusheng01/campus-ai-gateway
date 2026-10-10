# Qwen -> DeepSeek Failover Verification

This is the final environment test for M1.5-06.

The implementation deliberately does not add failover code to the Go gateway. The gateway renders ordered provider/model candidates and ThinkWatch Core performs runtime failover and circuit breaking.

## Preconditions

- Campus AI Gateway, PostgreSQL, Redis and ThinkWatch Core are running.
- `campus-smart` has at least two enabled routes:
  1. Qwen, priority 1
  2. DeepSeek, priority 2
- `DASHSCOPE_API_KEY` and `DEEPSEEK_API_KEY` are available to ThinkWatch.
- A valid `sk-campus-*` key can call `campus-smart`.
- The current database configuration has been synced to ThinkWatch.

## Verification

The script is intentionally opt-in because it temporarily replaces the primary provider URL with an unreachable loopback port. A `finally` block restores the provider and synchronizes ThinkWatch again.

```powershell
$env:ADMIN_TOKEN = "<admin-token>"
$env:CAMPUS_API_KEY = "sk-campus-..."

.\scripts\verify-failover.ps1 -Execute
```

Expected result:

```text
[PASS] Primary was unreachable and the virtual-model request still succeeded.
[OK] Primary provider restored.
```

This proves the business client kept using `campus-smart` while ThinkWatch moved the request from Qwen to the next configured route.

## Safety

If restoration fails, the script exits with code 2 and prints an explicit restore error. Restore the Qwen Provider Base URL in Admin UI and run “同步到 ThinkWatch” before any further test.

## Current verification status

The script and control-plane support are part of the repository. A real Qwen -> DeepSeek request still requires the final deployment environment and a valid DeepSeek credential; do not mark M1.5 failover as production-verified until this test passes there.
