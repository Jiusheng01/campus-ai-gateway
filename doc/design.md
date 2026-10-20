[← 返回 README](../README.md) · 数据面设计与路线图

本页讲的是网关内部形状与边界：整体架构与唯一的直连数据面（加权路由 / 熔断 / 逐次 attempt 遥测）、请求记录与管理审计的分离、当前已知限制，以及 Roadmap 与项目定位。

---

## 架构

```text
业务系统 / AI 应用
        │
        │  OpenAI-compatible API
        │  Authorization: Bearer sk-campus-...
        ▼
┌──────────────────────────────────────────────┐
│              Campus AI Gateway               │
│                                              │
│ Tenant / API Key / Model & IP Allowlist      │
│ RPM / TPM / Concurrency / Quota              │
│ Request ID / Usage / Audit                   │
│                                              │
│ Admin Session Auth / RBAC / Admin Audit      │
│ Embedded Admin UI                            │
│                                              │
│ Direct data plane (in-process)               │
│ Routing / Failover / Circuit Breaker / Guard │
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

Go Gateway 是校园侧控制面和访问控制边界。模型路由与数据面能力全部由网关自身实现（直连数据面，见下），不需要第二个进程，也没有需要同步的对象。

### 数据面：直连

**直连数据面是唯一的数据面**：网关按 `gateway_model_route` 解析对外模型，自己注入渠道凭据、流式透传，并按 priority 故障切换。网关因此只需要 PostgreSQL 和 Redis。

三项能力都已具备：

- **加权路由**：同一 `Route Priority` 内按 `Route Weight` 做 smooth weighted round robin，权重是真实分流比例（不再是排序用的 tiebreak）。
- **per-provider 熔断**：连续 5 次失败**或**最近 20 次内失败率 >50% 即 open；冷却 30s，失败的探测翻倍至 5 分钟上限；half-open 只放行一个探测。open 的渠道被跳过并在 attempt chain 里记为 `skipped`。熔断状态是**每实例、内存中**的。
- **逐次 attempt 遥测**：每次 Routing Attempt 都写入 `gateway_request_attempt`，管理台的请求详情展示完整 attempt chain（含被熔断跳过的线路）。

Admin UI 的「基础设施状态」面板第 4 项显示为「数据面」，模型连通测试由网关直接向渠道发起请求，并在结果中回报实际应答的渠道。

直连模式下的路由表缓存 5 秒：在 Admin UI 保存渠道 / 模型 / 线路后，最长 5 秒内生效，不需要任何同步动作。

数据面在自己的**响应**上多写一组 `X-Gateway-*` 响应头：`X-Gateway-Provider`、`X-Gateway-Upstream-Model`、`X-Gateway-Route-Priority`、`X-Gateway-Attempts`，分别记下这次请求实际用到的渠道、上游模型名、线路优先级和尝试次数。网关的请求日志再把它们读回来写入 `gateway_request_log`。这是数据面与日志器之间的**内部约定**，不是对外 API，业务调用方不需要关心。

---

## 请求与管理审计

业务 `/v1/*` 请求写入：

```text
gateway_request_log
```

主要字段包括：

- `request_id`
- `tenant_id`
- `api_key_id`
- `client_ip`
- `requested_model`
- `actual_model`
- `provider`
- `upstream_model`
- `route_priority`
- `attempts`
- `failover`
- Token usage
- latency
- HTTP status / error code
- `created_at`

管理员操作与安全事件分别写入独立审计表，避免和业务请求混在一起：

| 表 | 内容 |
| --- | --- |
| `gateway_request_log` | 业务 `/v1/*` 请求记录 |
| `gateway_admin_audit_log` | 管理员操作审计 |
| `gateway_security_event` | 安全事件 |

逐次 attempt 的明细单独落在 `gateway_request_attempt`（每次 Routing Attempt 一行：`attempt_no` / `provider` / `upstream_model` / `outcome` / `status_code` / `latency_ms` / `error_kind`），由 `request_id` 关联到请求记录。

请求记录里的 Provider / attempts 等字段不会由网关猜测：每次 Routing Attempt 由网关自己写入数据库，这些字段以这份记录为准。

---

## 当前已知限制

> 标注 **🧊 冻结** 的条目不在当前迭代范围内（见 README 的 Status 一节）。

- **Config Revision（ADR-0011）尚未实现**：Provider / Virtual Model / Route 没有 draft / 审批 / 原子激活，管理写入保存即生效。MS1 在模型级提供 `config_version` 乐观并发和整组线路原子保存，但这不是完整 revision 平台。
- **MS1 alias 服务端不可变**：`PUT /admin/models/{id}` 修改 alias 返回 `alias_immutable`；需要新名称时发布新 alias。MS1 不自动迁移调用方或 Key 白名单。
- **MS1 迁移历史是 one-way door**：数据库记录 0017 后，pre-MS1 二进制会因迁移目录缺少该版本而拒绝启动。回退需使用上一版本应用 + MS1 migration 目录，或恢复升级前备份；见 [MS1 运维说明](ms1-operations.md)。
- **`GET /v1/models` 已改为静态可路由 ∩ Key allowlist**：受限 Key 不再看到已禁用或不存在的 alias；这是 MS1 的明确兼容性变化。
- **keyring 轮换（ADR-0008）尚未实现**：内联 Credential 的密文带 `v1:` 版本前缀，但当前只写不读，也没有多密钥轮换或旧密文重加密。
- **迁移全量重放存在已知张力**：`0002` 为 `gateway_model.provider_id` 建了索引，而 `0014` 删除了该列，所以把整套迁移重放到一个已处于终态的数据库会在 `0002` 处失败。bootstrap 不要求每个历史文件都重放成功，而是先用 `verifyFinalSchema` 校验最终 schema 契约、通过后才写入 `schema_migrations`；这条容忍只在最终契约校验随每次新迁移同步更新时才成立。
- **ADR-0012 尚未实现的条款**：私有 CA / 自定义 RootCAs、Provider 客户端证书（mTLS）、请求体限额与 `413`（chat 16 MiB / 30s、管理 1 MiB / 10s）、把 `/v1` 面收敛为 `GET /v1/models` + `POST /v1/chat/completions`，以及 Kubernetes egress 第二层控制。
- **ADR-0012 已实现的部分**：`base_url` 在写入时必须是 `https://host[:port][/prefix]`，`http://` 仅在 `PROVIDER_HTTP_ALLOWLIST` 命中时允许；同一 scheme + allowlist 判断在**出站前**再执行一次，因此存量 `http://` 行同样会被拦截。每次连接都在 `Dialer.Control` 中以本次解析出的地址拒绝 loopback / link-local / multicast / unspecified（含 `0.0.0.0/8`）/ 云 metadata，DNS 结果随每次拨号复验。Provider 出站 transport 不使用 `HTTP(S)_PROXY` 等环境代理，避免代理地址让上述检查失效。自动重定向已禁止。发往 Provider 的请求头是白名单：只转发 `Accept`、`Content-Type`、W3C trace context（`traceparent` / `tracestate`）与 `X-Request-Id`；`Accept-Encoding` 固定为 `identity`（响应体原样流式透传），客户端值不转发；`Cookie`、`X-Forwarded-For`、`X-Real-IP` 等客户端头不再外泄；`Authorization` 由渠道 Credential 覆盖。被拒绝的目标对业务返回固定摘要与 `provider_egress_denied` 错误码，解析出的地址只写入运行日志与安全事件。
- 🧊 冻结：完整业务数据面的 Qwen -> Volcengine Ark Failover 仍需最终验收。
- 直连数据面的加权路由轮换与熔断状态是**每实例、内存中**的：多实例部署各自独立，重启只重置轮换与熔断；跨实例共享状态尚未实现。
- 🧊 冻结：学校真实 IdP 尚未完成现场集成验收。
- 目前没有管理员自助“忘记密码”与完整管理员生命周期 UI。
- `ADMIN_SESSION_IDLE_TTL` 尚未实现；当前主要使用 Refresh absolute TTL。
- 管理员 Access JWT 当前仍使用项目内 HS256 实现，后续可迁移成熟 JWT 库。
- 🧊 冻结：Multi-Gateway / PostgreSQL / Redis HA 尚未做目标环境故障切换验收。
- 🧊 冻结：Embedding / ASR 的真实 Provider 尚未完成最终验收。
- 🧊 冻结：30,000 RPM 仍必须在代表性生产拓扑实测，不能仅凭代码或本地压测标记通过。
- `/v1/*` 中除 `/v1/models` 以外的路径一律要求请求体带 `model` 字段，否则 `400 model_required`。这是为了不让"没有 model 的请求"绕过模型白名单；如果将来接入确实不含 `model` 的上游端点（如部分 ASR 接口），需要把该路径加入 `modelFreePaths` 例外表。
- 线路分组（`route_group`）字段保留在库表与 API 中但**数据面不读取**，因此已从 Admin UI 摘除；实现它需要一套除线路优先级之外的分组语义，故暂不实现。
- 月度预算（`monthly_budget_micros`）同样是**存而不执行**——没有单价数据就无法把 Token 用量折算成金额，字段保留会继续接受写入，但已从 Admin UI 摘除。真正的额度控制目前由 RPM / TPM / 最大并发承担。
- `allowed_models` 的语义在 2026-10-17 才修正为"`[]` = 一个都不允许"。迁移 `0011` 会把存量 `[]` 与 `null` 改写成 `["*"]` 以免影响已在用的 Key，但这意味着**升级后需要人工复核**：如果某个存量 Key 本意就是"不允许任何模型"，升级后它会变成"允许全部"。新建的 Key 不受影响。

---

## Roadmap

### 已完成

- [x] Provider / Model / Route 控制面
- [x] Tenant / 业务 API Key
- [x] RPM / TPM / 最大并发
- [x] 请求日志 / 管理审计 / 安全事件
- [x] Admin UI v3
- [x] 管理员 Session Auth v2
- [x] RBAC
- [x] trusted-header SSO 接入边界
- [x] 直连数据面
- [x] 多 Provider Route 控制面
- [x] Qwen 真实调用
- [x] Volcengine Ark 真实模型连通
- [x] Prometheus / readiness / production 示例
- [x] k6 性能验收工具
- [x] 渠道凭据变量命名空间约束（封堵 `gateway_admin` → `super_admin` 凭据读取提权链）
- [x] `allowed_models` 语义修正（`[]` = 全拒绝，`["*"]` = 全允许）+ 迁移 `0011` 存量回填
- [x] 租户停用真正生效（`403 tenant_disabled`）
- [x] `/v1/*` 模型字段改为 fail-closed 白名单，不再依赖端点黑名单
- [x] trusted-header 加固（`ADMIN_PROXY_SECRET` 最短长度 + `ADMIN_PROXY_CIDRS` 对端限制）
- [x] 直连数据面协议约束（只接受 `openai-chat`）
- [x] 加权路由（同一 priority 内 smooth weighted round robin，权重即真实分流比例）
- [x] per-provider 熔断（连续失败或错误率触发，跳过 open 渠道）
- [x] 逐次 attempt 遥测 + attempt chain UI（`gateway_request_attempt`）
- [x] 响应头改名：`X-ThinkWatch-*` → `X-Gateway-*`（退役 ThinkWatch 后不再读取旧名）
- [x] ThinkWatch 退役：数据面只剩直连，不再需要第二个进程与状态同步
- [x] CI 为测试任务提供 PostgreSQL / Redis service，CI 绿与本地绿覆盖一致
- [x] MS1 模型服务管理闭环：上游渠道、候选模型、对外模型发布、整组路由保存、模型级 Key 授权、`/v1/models` 交集、脱敏运行摘要、影响预览与缓存代次
- [x] MS1 迁移 0017 + 升级/回退演练、Vitest 前端回归与 CI 测试步骤

### 下一阶段

- [ ] 管理员生命周期管理 / 密码恢复
- [ ] Secret Manager / KMS 接入

#### 冻结（暂不排期）

- [ ] 完整业务数据面 Qwen -> Ark Failover 验收
- [ ] 学校真实 IdP 接入验收
- [ ] Multi-Gateway / PostgreSQL / Redis HA
- [ ] Embedding / ASR 真实 Provider
- [ ] 30,000 RPM 正式性能验收

---

## 项目定位

本仓库不是完整的“校园 AI 助手 / 智能体 / 知识库”产品。

它负责的是校园 AI 平台中更底层、也更关键的一层：

> **统一模型接入 + 访问控制 + 路由治理 + 配额 + 安全审计 + 管理控制面。**

当前项目已经进入：

> **可运行、可管理、可真实调用、可多 Provider 配置、可认证授权、可限流、可审计的校园大模型统一接入网关。**

接下来重点从“继续堆功能”转向真实生产环境的 **Failover、SSO、HA、Telemetry 与性能验收**。
