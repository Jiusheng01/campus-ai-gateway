# Campus AI Gateway 开发计划

> 版本：v1.0  
> 状态：生效中  
> 基线：M1 已完成并验证  
> 原则：除非发现架构级问题，否则按本计划顺序推进。

## 1. 项目范围

本仓库聚焦校园 AI 平台的模型基础设施层：

- 大模型统一接入
- 模型路由
- Tenant 与 Virtual API Key 管理
- 限流与配额
- 安全治理
- 请求审计
- 模型运行时可观测性

本仓库**不负责**重建完整的校园 AI 门户、知识库、Agent 平台、智能客服或工作流平台。

## 2. 稳定基线

当前稳定基线包括：

- Go Gateway
- PostgreSQL
- Redis
- ThinkWatch Core
- Qwen Provider
- OpenAI-compatible API
- `campus-smart` 虚拟模型
- Tenant 管理
- Virtual API Key
- Model Allowlist
- IP Allowlist
- RPM
- TPM 基础控制
- 最大并发
- SSE 代理
- Request ID
- Token Usage 采集
- Request Audit
- Provider 管理
- Model 管理
- Embedded Admin UI
- ThinkWatch 配置同步
- 网页模型测试
- Windows setup / dev / status / stop 脚本

已验证端到端链路：

```text
Tenant
  -> sk-campus-* Virtual Key
  -> Go Gateway
  -> Model/IP 鉴权
  -> RPM/TPM/Concurrency
  -> ThinkWatch Core
  -> campus-smart
  -> Qwen
  -> OpenAI-compatible Response
  -> Token/Latency/Status Audit
  -> PostgreSQL
```

后续开发不得破坏这条基线。

## 2.1 实现状态 - 2026-10-10

规划中的仓库侧工作已经推进到 M4 基础阶段。

| 区域 | 仓库实现状态 | 最终环境验收 |
| --- | --- | --- |
| M1.5 Route Schema / CRUD / Admin UI | 已完成 | Qwen 基线已真实验证 |
| M1.5 DeepSeek / 多 Provider Renderer | 已完成 | 需要真实 DeepSeek 凭据 |
| M1.5 Qwen → DeepSeek Failover | 验证脚本已完成 | 双 Provider 真实运行待验收 |
| M1.6 Route Audit | Schema / Capture 契约已完成 | Provider / Attempt Telemetry Bridge 待验收 |
| M2.0 Security / Admin Audit | 已完成 | 最终组合回归待验收 |
| M2.1 SSO / RBAC | Gateway trusted-header / RBAC 已完成 | 学校 IdP 待接入 |
| M2.2 TPM / Tenant Quota | Reserve / Reconcile + Redis Tenant Enforcement 已完成 | 最终配额场景待验收 |
| M2.3 Admin UI 产品化 | 当前计划范围已完成 | 浏览器回归待验收 |
| M3.0 性能 / 生产工具 | k6 / Health / Readiness / HTTPS 指南已完成 | 30,000 RPM Benchmark 待验收 |
| M3.1 HA / Monitoring | Metrics / Prometheus / HA 指南已完成 | 基础设施 Failover 待验收 |
| M4.0 模型服务基础 | Embedding + Multipart ASR Gateway 基础已完成 | 真实 Provider 请求待验收 |

这里的“已完成”表示代码/工具已经进入仓库，并在适用位置接受 CI 校验；**不等于外部 Provider、学校 SSO、HA 或生产压测已经实际通过**。

## 3. Milestone

| Milestone | 目标 | 优先级 |
| --- | --- | --- |
| M1.5 | 多 Provider 模型路由与 Failover | P0 |
| M1.6 | Route Audit 与可观测性 | P0 |
| M2.0 | Security Event 与 Admin Audit | P0 |
| M2.1 | SSO 与 RBAC | P0 |
| M2.2 | 精确配额与 Token 核算 | P1 |
| M2.3 | Admin UI 产品化 | P1 |
| M3.0 | 性能验证与生产硬化 | P0 |
| M3.1 | 高可用与监控 | P1 |
| M4.0 | Embedding / ASR / 扩展模型服务 | P1 |

## 4. M1.5 - 多 Provider Routing 与 Failover

### 4.1 数据模型

原始模型：

```text
gateway_model
  alias
  provider_id
  upstream_model
```

目标模型：

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

示例：

```text
campus-smart
  P1 -> qwen      -> qwen3.7-flash-...
  P2 -> deepseek  -> deepseek-chat
  P3 -> glm       -> glm-...
```

### 4.2 Migration

新增：

```text
migrations/0003_model_routes.sql
```

规则：

- 不修改已经发布的 Migration。
- 自动把旧 Model / Provider 绑定迁移为第一条 Route。
- 不停机保护现有 Qwen 路径。

### 4.3 Provider 接入顺序

1. Qwen - 已完成
2. DeepSeek
3. GLM
4. Kimi
5. Doubao

不要一次性同时接入全部 Provider。先完成 Qwen + DeepSeek 的真实 Failover 验证。

### 4.4 M1.5 完成定义

正常路径：

```text
campus-smart
  -> Qwen
  -> 200
```

故障路径：

```text
campus-smart
  -> Qwen
  -> failure
  -> DeepSeek
  -> 200
```

只有两条真实请求路径都通过后，M1.5 才算完成环境验收。

## 5. M1.6 - Route Audit 与可观测性

请求审计扩展为：

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

Migration：

```text
migrations/0004_route_audit.sql
```

期望日志：

```text
requested_model = campus-smart
provider        = deepseek
upstream_model  = deepseek-chat
route_priority  = 2
attempts        = 2
failover        = true
status          = 200
```

## 6. M2.0 - Security Event 与 Admin Audit

业务请求日志与管理员操作日志必须分离。

新增：

```text
gateway_admin_audit_log
gateway_security_event
```

安全事件包括：

- invalid_api_key
- model_forbidden
- ip_forbidden
- rpm_exceeded
- tpm_exceeded
- concurrency_exceeded
- provider_failure
- circuit_open
- guard_blocked

管理审计示例：

- Provider 创建
- Route Priority 修改
- Model 停用
- Tenant 停用
- API Key 停用
- ThinkWatch 配置同步

## 7. M2.1 - SSO 与 RBAC

当前 `ADMIN_TOKEN + localStorage` 只允许用于开发。

目标：

```text
Campus SSO
  -> Admin Login
  -> Session
  -> User
  -> Role
  -> Permission
```

初始角色：

| Role | 权限范围 |
| --- | --- |
| super_admin | 全部权限 |
| gateway_admin | Provider / Model / Route |
| tenant_admin | Tenant 管理 |
| auditor | 只读审计 |
| operator | 运行状态 / 模型测试 |

## 8. M2.2 - 精确配额核算

TPM 目标流程：

```text
request
  -> estimate tokens
  -> reserve
  -> model request
  -> receive actual usage
  -> reconcile
  -> Redis correction
```

Tenant 统计包括：

- 日请求数
- 月请求数
- 日 Token
- 月 Token
- Budget / Quota
- Estimated Cost
- 当前 RPM
- 错误率

## 9. M2.3 - Admin UI 产品化

后端行为稳定前，不重写 Admin UI。

计划 Dashboard 指标：

- 请求趋势
- Token 趋势
- Provider 成功率
- P50 / P95 / P99
- Failover 次数
- 错误率
- Tenant Top N

Request Log 筛选：

- 时间范围
- Tenant
- Provider
- Model
- HTTP Status
- Error Code
- Failover
- Request ID
- Pagination
- Detail
- Export

## 10. M3.0 - 性能验证

目标：

```text
30,000 RPM ~= 500 requests/second
```

必须分别测试：

- `/v1/models`
- Chat 非流式
- SSE
- Virtual Key Auth
- RPM
- TPM
- 最大并发
- PostgreSQL Audit
- Provider 正常路径
- Provider Failover
- Burst Traffic

记录：

- RPS
- P50
- P95
- P99
- Error Rate
- CPU
- Memory
- Redis OPS
- DB Connections
- Goroutines

在可重复 Benchmark 产生前，不得把 30,000 RPM 标记为完成。

## 11. M3.1 - 生产架构

目标：

```text
Load Balancer
  -> Gateway 1
  -> Gateway 2
  -> ThinkWatch
  -> Providers

Gateway -> Redis HA/Cluster
Gateway -> PostgreSQL HA
```

生产任务：

- HTTPS
- Secret Management
- Log Rotation
- Metrics
- Prometheus
- Grafana
- Alerts
- Backup / Restore
- Health Check
- Graceful Shutdown
- PostgreSQL HA
- Redis HA

## 12. 团队工作流

建议分工：

| 工作流 | 区域 |
| --- | --- |
| A | Go Gateway / Control Plane |
| B | ThinkWatch / Routing / Failover |
| C | Admin UI |
| D | PostgreSQL / Redis / Audit |
| E | QA / Security / Performance |

M1.5 并行示例：

- A：`0003_model_routes.sql` + Model Route API
- B：ThinkWatch Route Renderer + Failover 验证
- C：Route 管理 UI
- D：Route Audit Schema / Design
- E：回归与故障路径测试

## 13. Git 协作规则

不要长期直接在 `main` 开发。

分支命名：

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

流程：

```text
main
  -> feature/*
  -> Pull Request
  -> Review
  -> go test ./...
  -> Merge
```

原则：

```text
一个 PR = 一个完整、单一的功能主题
```

避免把无关的数据库、UI、认证、路由、脚本修改塞进同一个 PR。

## 14. 数据库 Migration 规则

顺序：

```text
0001_init.sql
0002_management.sql
0003_model_routes.sql
0004_route_audit.sql
0005_security_events.sql
...
```

规则：

- 只追加 Migration
- 不重写团队或环境已经执行过的 Migration
- 在可行情况下保证幂等
- 每个 Schema 变化必须包含升级验证

## 15. API 兼容原则

业务系统正常情况下只使用虚拟模型名：

```text
campus-smart
```

不要让业务系统直接绑定：

```text
qwen3.7-...
deepseek-chat
glm-...
```

目标：

```text
Business App
  -> campus-smart
  -> Gateway Policy
  -> Qwen / DeepSeek / GLM / Kimi
```

这样才能保持透明切换和 Failover。

## 16. Definition of Done

功能不是“代码写完”就算完成。

要求：

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

对于 Provider / Model：

- 真实 Provider 请求必须通过。

对于 Routing：

- 正常路径必须通过。
- 故障 / Failover 路径必须通过。

对于性能：

- 必须保存可重复的 Benchmark 报告。

## 17. 固定开发顺序

除非遇到架构问题需要重新规划，否则按以下顺序：

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

## 18. 下一步

下一步不再是继续堆基础功能，而是进行统一最终环境验收：

```text
docs/FINAL_ACCEPTANCE.md
```

优先验收：

1. DeepSeek 真实请求。
2. Qwen → DeepSeek 真实 Failover。
3. ThinkWatch 实际 Route Telemetry Bridge / 验证。
4. 学校 SSO + RBAC。
5. Tenant Quota 回归。
6. 按需求验证 Embedding / ASR 真实 Provider。
7. Multi-instance / PostgreSQL / Redis HA。
8. 30,000 RPM ~= 500 RPS 可重复 Benchmark 报告。

没有真实测试证据前，不允许把依赖环境的需求标记为完成。
