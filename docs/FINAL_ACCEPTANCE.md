# 最终统一验收清单

本文档用于项目负责人在开发阶段完成后，进行一次性的人工/环境验收。开发过程中的 CI、单元测试、Build 和静态检查应在此之前完成。

## 1. 更新并启动

```powershell
cd D:\AI\campus-ai-gateway
git switch main
git pull
.\scripts\dev.ps1 -ForceRestart
```

预期：

- PostgreSQL 正常
- Redis 正常
- Campus Gateway 正常
- ThinkWatch Core 正常
- Admin UI 可以打开
- migration 0001～0006 可以重复执行且无错误

## 2. Qwen 基线回归

使用一个已经授权 `campus-smart` 的 `sk-campus-*` Key 验证：

- GET `/v1/models` 只返回允许的虚拟模型
- 非流式 Chat 返回 HTTP 200 和 usage
- SSE Chat 可以持续流式返回，不被 Gateway 缓冲
- Request Log 包含 Request ID、Token、延迟和状态
- RPM / TPM / 最大并发 / 模型权限 / IP 权限拒绝行为仍符合预期

## 3. 多 Provider Route 配置

在 Admin UI 中：

1. 添加或确认 Qwen Provider。
2. 使用“添加 DeepSeek”，并确认 ThinkWatch 运行进程环境中存在 `DEEPSEEK_API_KEY`。
3. 打开 `campus-smart -> 管理线路`。
4. 配置：
   - Qwen：priority = 1
   - DeepSeek：`deepseek-chat`，priority = 2
5. 点击同步到 ThinkWatch。
6. 执行模型测试。

## 4. 真实 Failover

设置可用的业务 Key：

```powershell
$env:ADMIN_TOKEN = "<admin-token>"
$env:CAMPUS_API_KEY = "sk-campus-..."
.\scripts\verify-failover.ps1 -Execute
```

期望输出：

```text
[PASS] Primary was unreachable and the virtual-model request still succeeded.
[OK] Primary provider restored.
```

如果通过，可把 M1.5 的 Qwen → DeepSeek 真实故障转移标记为“环境已验证”。

## 5. Route Audit

确认请求日志能够展示：

- `requested_model`
- `actual_model`
- `provider`
- `upstream_model`
- `route_priority`
- `attempts`
- `failover`
- Token / latency / status

注意：当前检查到的 ThinkWatch Core 版本没有通过普通 HTTP Response Header 暴露最终 Route 事实。

因此：

- 只有在兼容的 Telemetry Bridge / Header 契约存在时，Provider / Attempts 等字段才会有实际值。
- 如果为空，不代表 Gateway 本身失败。
- 不允许用“计划路由”猜测或伪造“实际路由”。

## 6. Security Event 与 Admin Audit

主动触发：

- 无效 API Key
- 过期/禁用 Key
- 模型越权
- IP 越权
- RPM 超限
- TPM 超限
- 最大并发超限
- Tenant 月请求配额超限
- Tenant 日/月 Token 配额超限

检查 Admin UI 的“安全事件”。

然后执行以下管理操作：

- 修改 Provider
- 修改 Model
- 修改 Route
- 修改 Tenant
- 修改 API Key
- 同步 ThinkWatch

检查“管理审计”。

## 7. SSO / RBAC

开发模式：

```text
ADMIN_AUTH_MODE=token
```

生产验收应置于学校 OIDC 认证层之后：

```text
Campus IdP
  -> oauth2-proxy
  -> Gateway trusted-header
  -> RBAC
```

分别验证：

- `super_admin`
- `gateway_admin`
- `tenant_admin`
- `auditor`
- `operator`

还要验证：

> 绕过可信反向代理直接访问 Gateway 时，不能伪造管理员身份 Header。

## 8. 配额核算

给 Tenant 配置较小额度并验证：

- 月请求额度达到上限后返回 429
- 日/月 Token 配额耗尽后返回 429
- 正常成功请求如果返回 usage，估算值会按实际 Token 对账
- Admin UI “用量”能看到日/月请求量和 Token

金额预算当前只作为配置/报表基础。

在没有权威 Provider 价格和真实 Route Telemetry 之前，不进行猜测性的金额扣费。

## 9. Admin UI 产品化功能

验证：

- Route CRUD
- Dashboard P95
- Failover 指标
- Provider 指标
- Request Log 筛选
- 分页
- CSV 导出
- Tenant 用量
- 管理审计
- 安全事件

## 10. Embedding / ASR 基础

如果当前 ThinkWatch / Provider Adapter 支持目标服务，验证：

- 使用虚拟模型调用 `POST /v1/embeddings`
- 使用 multipart + 虚拟模型调用 `POST /v1/audio/transcriptions`

Gateway 已负责：

- 鉴权
- 模型权限
- 限流
- 透明转发

Provider 真实调用仍属于最终环境验收项。

## 11. 生产健康检查与监控

检查：

- `/healthz`
- `/readyz`
- `/metrics`
- Prometheus Scrape
- HTTPS Edge 配置
- Secret 从外部注入
- Backup / Restore Runbook
- 多 Gateway readiness 行为

## 12. 30,000 RPM / 500 RPS

按照 `docs/PERFORMANCE_TEST.md` 执行。

至少运行：

```powershell
k6 run .\loadtest\k6-models.js
k6 run .\loadtest\k6-chat.js
```

在代表性环境设置：

```text
TARGET_RPS=500
```

在 Benchmark 报告中记录以下数据之前，不允许把 30,000 RPM 标记为已通过：

- 实际达到的 RPS
- P50 / P95 / P99
- 错误率
- CPU
- 内存
- Redis
- PostgreSQL
- Gateway
- ThinkWatch

## 最终验收总结

以下项目依赖真实环境，不能仅靠仓库 CI 宣称完成生产验收：

- DeepSeek 真实凭据 / 真实请求
- Qwen → DeepSeek 真实 Failover
- ThinkWatch 实际 Route Telemetry
- 学校 IdP SSO
- 生产 HA / Failover
- 真实 Embedding / ASR Provider
- 30,000 RPM Benchmark
