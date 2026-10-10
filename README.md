# Campus AI Gateway

校园大模型统一接入、模型路由、限流与安全审计网关。

本项目定位在校园 AI 平台的 **模型网关 / 模型路由 / 访问控制 / 审计** 层：对上提供 OpenAI-compatible API，对下通过 ThinkWatch Core 接入 Qwen 等模型服务，并在 Go Gateway 中承担租户、Virtual API Key、配额、模型权限与请求审计。

> 当前状态：**M0/M1 基线已真实验收；M1.5～M4 的网关侧实现、管理面、审计、SSO/RBAC 接入边界、配额、监控与压测工具已进入代码基线。真实 DeepSeek/Failover、学校 IdP、HA 与 30,000 RPM 仍需最终环境统一验收。**
>
> 已真实验证：Virtual Key → Go Gateway → ThinkWatch Core → 阿里百炼 Qwen → Token Usage → PostgreSQL 审计日志。

---

## 1. 当前能力

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| OpenAI-compatible API | ✅ | /v1/models、/v1/chat/completions |
| Virtual API Key | ✅ | sk-campus-*，HMAC-SHA256 摘要存储 |
| 租户管理 | ✅ | 新增、编辑、启用/停用 |
| Key 状态 / 过期校验 | ✅ | 支持禁用和过期时间 |
| 模型白名单 | ✅ | 每个 Key 独立授权模型 |
| IP 白名单 | ✅ | 每个 Key 独立配置 |
| RPM 限流 | ✅ | Redis |
| TPM 控制 | ✅ | 当前为请求前预占式 |
| 最大并发控制 | ✅ | Redis |
| Request ID | ✅ | X-Request-Id |
| SSE 透明代理 | ✅ | 支持流式返回，不缓冲客户端响应 |
| Token Usage 采集 | ✅ | 非流式 + OpenAI SSE 最终 usage chunk |
| 请求审计 | ✅ | PostgreSQL gateway_request_log |
| ThinkWatch Core 接入 | ✅ | HTTP 数据面接入 |
| Qwen 真实调用 | ✅ | campus-smart 已完成端到端验收 |
| Provider 管理 | ✅ | 新增、编辑、启用/停用 |
| Model 管理 | ✅ | 虚拟模型 → Provider → 上游模型 |
| ThinkWatch 配置同步 | ✅ | 生成前校验、备份、原子替换 |
| 网页模型测试 | ✅ | 后台直接测试模型、Token、延迟 |
| Admin UI v2 | ✅ | React + TypeScript + Vite；构建后嵌入 Go，运行时无需 Node 进程 |
| Windows 一键初始化 | ✅ | scripts/setup.ps1 |
| Windows 一键开发启动 | ✅ | scripts/dev.ps1 |
| 多 Provider Failover 控制面 | ✅ | Route CRUD/UI + ThinkWatch renderer；真实双 Provider 验收待最终测试 |
| 实际 Provider / 上游模型回填 | 🧪 | 审计字段 + telemetry 采集契约完成；ThinkWatch 最终 telemetry 待验收 |
| SSO / RBAC | ✅* | trusted-header + RBAC 完成；学校真实 IdP 待最终验收 |
| 30,000 RPM 压测验收 | ⏳ | k6 500 RPS 工具完成；真实目标环境尚未压测 |

---

## 2. 架构

~~~text
业务系统 / AI 应用
        │
        │ OpenAI-compatible API
        ▼
┌────────────────────────────────────────────┐
│            Campus AI Gateway               │
│                                            │
│ Tenant / Virtual Key                       │
│ HMAC Auth / Expire / Status                │
│ Model & IP Allowlist                       │
│ RPM / TPM / Concurrency                    │
│ Request ID / Audit / Usage                 │
│ Admin API / Embedded Admin UI              │
└──────────────────┬─────────────────────────┘
                   │
                   ▼
┌────────────────────────────────────────────┐
│              ThinkWatch Core               │
│ Routing / Failover / Circuit Breaker / Guard│
└──────────────────┬─────────────────────────┘
                   │
                   ▼
           LLM Providers
       Qwen / DeepSeek / ...

Data:
  PostgreSQL -> tenants / keys / providers / models / audit
  Redis      -> RPM / TPM / concurrency
~~~

当前阶段 **Go Gateway 是校园侧控制面和访问控制边界**，ThinkWatch Core 负责底层模型数据面能力。项目不直接复制 ThinkWatch Core 源码，避免过早深改 Rust 核心。

---

## 3. 已完成真实验收

### 3.1 业务链路

已实际验证：

~~~text
sk-campus-* Virtual Key
        ↓
租户 / Key 鉴权
        ↓
模型白名单
        ↓
RPM / TPM / 并发控制
        ↓
Go Gateway
        ↓
ThinkWatch Core
        ↓
campus-smart
        ↓
Qwen
        ↓
OpenAI-compatible Response
        ↓
Token / latency / HTTP status
        ↓
PostgreSQL 审计
~~~

真实请求已返回：

- model = campus-smart
- HTTP 200
- prompt / completion / total tokens
- 请求延迟
- 对应 Request ID
- 请求日志落库

### 3.2 已验证异常路径

- 错误 Virtual Key → 401
- 模型越权 → 403
- RPM 超限 → 429
- 并发超限 → 429
- SSE 流式返回正常
- ThinkWatch Provider 凭据缺失时能返回明确错误

---

## 4. 内置管理后台

启动 Gateway 后访问：

~~~text
http://127.0.0.1:8080/admin-ui
~~~

当前页面：

~~~text
校园模型网关
├── 总览
│   ├── Gateway
│   ├── PostgreSQL
│   ├── Redis
│   ├── ThinkWatch
│   ├── 今日请求
│   ├── 今日 Tokens
│   ├── 今日错误
│   └── 平均延迟
├── Provider 管理
├── 模型管理
│   ├── 同步到 ThinkWatch
│   └── 测试模型
├── 租户管理
├── API Key 管理
└── 请求日志
~~~

管理 API 与业务 Virtual Key 分离：

- 管理端：ADMIN_TOKEN
- 业务端：sk-campus-*

当前后台已升级为 React + TypeScript + Vite 前端工程，构建后静态资源继续嵌入 Go Gateway，因此生产运行时不需要额外 Node 服务。

主要界面：

- 可收缩侧栏
- 浅色 / 深色主题
- 系统健康状态横幅
- Gateway / PostgreSQL / Redis / ThinkWatch 状态
- 今日请求、Token、错误、平均延迟、P95、Failover 指标
- Provider 管理
- 虚拟模型 + Route Failover 链可视化
- Tenant 配额和实际用量
- API Key 管理
- 请求日志筛选 / 分页 / CSV / 详情 Drawer
- 管理审计
- 安全事件
- 当前身份与认证模式

本地开发 `ADMIN_AUTH_MODE=token` 时，可把 ADMIN_TOKEN 保存到浏览器 localStorage。

> localStorage Token 仅用于本地开发。生产 `trusted-header` 模式由校园 OIDC / oauth2-proxy 等可信反向认证层注入身份，Admin UI 不要求浏览器保存管理员长期 Token。

详见 `docs/ADMIN_UI.md`。

---


### Admin UI v2 构建

本地开发需要 Node.js 22+（仅构建阶段）：

~~~powershell
npm --prefix .\web\admin install
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
~~~

日常使用 `scripts/dev.ps1` 时会自动完成前端构建，然后再启动 Go Gateway。

## 5. Windows 本地开发

### 5.1 第一次：一键初始化

~~~powershell
cd D:\AI\campus-ai-gateway
.\scripts\setup.ps1
~~~

脚本会：

- 检查 Go / Docker
- 自动寻找 twcore.exe
- 保存 TWCORE_EXE
- 设置 THINKWATCH_CONFIG_PATH
- 校验 ThinkWatch 配置
- 从 ThinkWatch clients 配置识别 tw-* Gateway Key
- 保存 THINKWATCH_GATEWAY_KEY
- 检查 DASHSCOPE_API_KEY
- 缺少 Provider Key 时使用隐藏输入
- 自动启动开发环境

如果 ThinkWatch Core 不在常见目录：

~~~powershell
.\scripts\setup.ps1 -TwCorePath "D:\AI\ThinkWatch-Core\twcore.exe"
~~~

只初始化、不启动：

~~~powershell
.\scripts\setup.ps1 -NoStart
~~~

### 5.2 日常开发

以后通常只需要：

~~~powershell
.\scripts\dev.ps1
~~~

如果 8080 上有旧 Gateway：

~~~powershell
.\scripts\dev.ps1 -ForceRestart
~~~

查看状态：

~~~powershell
.\scripts\status.ps1
~~~

停止 Gateway + PostgreSQL + Redis：

~~~powershell
.\scripts\stop.ps1
~~~

---

## 6. Provider 与模型管理

Provider 当前保存：

- 名称
- Base URL
- 协议
- Provider 凭据环境变量名
- 启用状态

例如 Qwen：

~~~text
name:           qwen
protocol:       openai-chat
credential_env: DASHSCOPE_API_KEY
status:         enabled
~~~

**真实 Provider API Key 不保存到 PostgreSQL，也不通过管理页面持久化。**

当前数据模型：

~~~text
virtual model alias
    ↓
gateway_model_route (1..N)
    ↓
provider + upstream model
    ↓
priority / weight / timeout / status
~~~

已验证示例：

~~~text
campus-smart
    ↓
qwen
    ↓
qwen3.7-flash-2026-07-15
~~~

管理后台“模型管理”提供 **同步到 ThinkWatch**。

同步过程：

~~~text
PostgreSQL Provider / Model
        ↓
生成临时 config
        ↓
twcore check
        ↓
校验成功
        ↓
备份 config.yaml.campus.bak
        ↓
原子替换 config.yaml
        ↓
ThinkWatch reload
~~~

只更新 ThinkWatch 顶层 providers / aliases，保留 listen / clients 等其它配置。

Windows 默认配置路径：

~~~text
%APPDATA%\ThinkWatch\config.yaml
~~~

---

## 7. 租户与 Virtual API Key

关系：

~~~text
Tenant
├── Key A
├── Key B
└── Key C
~~~

同一部门可以按业务系统拆分多个 Key，各自配置：

- 模型白名单
- IP 白名单
- RPM
- TPM
- 最大并发
- 有效期
- 启用 / 停用

完整 Key 形如：

~~~text
sk-campus-...
~~~

完整 Key **只在创建时显示一次**。数据库仅保存 key_prefix 和 HMAC-SHA256 key_digest，不保存完整明文 Key。

---

## 8. API 调用

### Models

~~~bash
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-campus-..."
~~~

### Chat Completions

~~~bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [
      {"role": "user", "content": "你好"}
    ],
    "stream": false
  }'
~~~

---

## 9. 请求审计

认证通过的 /v1/* 请求写入 gateway_request_log。

当前记录：

- request_id
- tenant_id
- api_key_id
- client_ip
- requested_model
- actual_model
- provider
- stream
- input tokens
- output tokens
- total tokens
- latency_ms
- status_code
- error_code
- created_at

其中 provider 字段已经预留，但当前还没有从 ThinkWatch telemetry 中可靠回填真实 Provider。

### 当前已知限制

- TPM 当前是**预占式**，不是最终 usage 的严格实时结算。
- 请求日志中的真实 Provider / 最终上游模型还需结合 ThinkWatch telemetry / trace 回填。
- 管理后台请求日志目前是基础列表，还未完成高级筛选、分页、导出。

---

## 10. 目录

~~~text
cmd/
├── server/              Gateway HTTP 服务入口
└── keygen/              Virtual Key 工具

internal/
├── app/                 Server / middleware / Admin API / embedded UI
│   └── adminui/dist/    Admin UI v2 Vite 构建产物
├── httpx/               HTTP 公共能力
├── model/               数据结构
├── proxy/               ThinkWatch 透明代理
├── ratelimit/           Redis 限流
├── security/            Virtual Key HMAC
└── storage/             PostgreSQL / Redis

migrations/
├── 0001_init.sql
├── 0002_management.sql
├── 0003_model_routes.sql
├── 0004_route_audit.sql
├── 0005_security_events.sql
└── 0006_quota.sql

deploy/
├── docker-compose.yml
├── docker-compose.dev.yml
└── nginx/

web/
└── admin/               React + TypeScript + Vite 管理后台源码

scripts/
├── setup.ps1
├── dev.ps1
├── status.ps1
└── stop.ps1
~~~

---

## 11. 与校园 AI 项目需求的对应关系

本仓库不是完整“校园 AI 助手门户 / 智能体 / 知识库”产品，而是其中的 **统一模型接入和安全治理基础设施**。

| 校园 AI 需求方向 | 本项目当前状态 |
| --- | --- |
| 主流大模型统一接入 | Qwen 已真实接通；其它 Provider 待接入 |
| 模型管理 | Provider / Model 后台已完成 |
| 模型自由切换基础 | 虚拟模型 Alias 已完成；多 Provider 绑定待完成 |
| 调用权限管理 | Tenant + Virtual Key + Model/IP Allowlist 已完成 |
| 高并发限流 | RPM / TPM / Concurrency 已实现；目标压测未完成 |
| 调用日志 | Request ID / Token / latency / status 已完成 |
| 本地化部署基础 | Go + PostgreSQL + Redis + ThinkWatch 本地运行 |
| 安全审计 | Key 摘要存储 + 请求审计已完成 |
| 角色权限 | 尚未完成 SSO / RBAC |
| 多模型 Failover | ThinkWatch 具备底层能力；本项目控制面配置尚在开发 |
| 30,000 RPM | **尚未完成目标压测，不能视为已满足** |
| HTTPS / 正式安全部署 | Nginx 基础存在，生产硬化尚未完成 |

---

## 12. Roadmap

### M1：模型控制面

- [x] Provider 管理
- [x] Model 管理
- [x] Tenant 管理
- [x] Virtual Key 管理
- [x] ThinkWatch 配置同步
- [x] 网页模型测试
- [x] 一个虚拟模型绑定多个 Provider
- [x] Provider 优先级
- [x] Failover 路由控制面（真实双 Provider 最终验收待执行）
- [ ] Circuit Breaker 状态展示
- [x] 审计 Schema/采集契约（ThinkWatch 最终 telemetry 待验收）

### M2：安全与运营

- [x] 学校统一身份认证接入边界（trusted-header / external OIDC；真实 IdP 待验收）
- [x] RBAC
- [x] 管理员操作日志
- [x] 安全事件
- [ ] Guard / 敏感策略事件展示
- [x] 审计筛选 / 分页 / 导出
- [x] 租户维度统计

### M3：性能与生产化

- [x] TPM usage reconcile（无 usage 时保留估算）
- [ ] 分布式部署验证
- [ ] 30,000 RPM 目标压测
- [ ] 突发流量压测
- [ ] SSE 高并发压测
- [x] HTTPS 生产示例配置（证书/域名最终环境提供）
- [ ] Secret Manager / KMS
- [ ] PostgreSQL / Redis 高可用
- [x] Prometheus 指标/配置 + 容量验收文档（真实告警平台最终环境配置）

---

## 13. 安全说明

本地开发默认值仅用于开发。

正式部署前必须替换：

- ADMIN_TOKEN
- API_KEY_HMAC_SECRET
- PostgreSQL 密码
- Redis 安全配置
- Provider credentials

生产环境不得继续使用浏览器 localStorage 作为管理员长期认证方案，应接入：

~~~text
学校统一身份认证
        ↓
后台登录 Session
        ↓
RBAC
        ↓
管理操作审计
~~~

Provider API Key 推荐通过环境变量、Secret Manager、KMS / Vault 管理，不应写入代码仓库或业务数据库。

---

## 14. 当前结论

项目已经从概念验证进入：

> **可运行、可管理、可真实调用、可限流、可审计的校园大模型统一接入网关。**

当前最重要的下一步：

> **当前开发阶段已转入最终统一验收：真实 DeepSeek/Failover、ThinkWatch route telemetry、学校 SSO、HA 与 30,000 RPM。**

---

## 15. 2026-10-10 Roadmap 实现更新

当前代码已经继续完成了原 Development Plan 的主要网关侧工作：

- **M1.5 多 Provider 控制面**：`gateway_model_route`、Route CRUD、Admin Route UI、DeepSeek Provider preset、ThinkWatch 多 Provider route renderer、可恢复的 Qwen -> DeepSeek Failover 验证脚本。
- **M1.6 路由审计**：新增 `upstream_model / route_priority / attempts / failover`。Gateway 支持 ThinkWatch route telemetry Header 采集契约；当前 ThinkWatch Core 版本未提供普通 HTTP Header 形式的最终路由事实时，字段保持 unknown，不会猜测实际 Provider。
- **M2.0 安全与管理审计**：`gateway_admin_audit_log`、`gateway_security_event`，业务请求日志与管理员操作分离。
- **M2.1 SSO/RBAC**：保留本地 `ADMIN_TOKEN` 开发模式；生产支持 trusted-header，推荐由成熟 `oauth2-proxy`/等价 OIDC 组件负责学校登录与 Session，Gateway 负责角色授权。角色包括 `super_admin / gateway_admin / tenant_admin / auditor / operator`。
- **M2.2 配额**：API Key TPM 已从纯预占升级为 `estimate -> reserve -> actual usage -> reconcile`；Tenant 支持月请求、日/月 Token Redis 共享配额与实际 usage 对账；金额预算字段仅用于配置/报表，不在缺少权威价格与实际 Route telemetry 时猜测扣费。
- **M2.3 Admin UI**：Dashboard P95/Failover/Provider 指标、Route 管理、Tenant quota/用量、请求日志筛选/分页/CSV、管理审计、安全事件。
- **M3.0/M3.1**：`/healthz`、`/readyz`、`/metrics`，Prometheus 配置、HTTPS 双 Gateway Nginx 示例、k6 500 RPS 压测脚本和生产/HA 指南。**这不等于 30,000 RPM 已通过，必须在目标环境实测。**
- **M4.0 基础**：`/v1/embeddings` 继续走虚拟模型权限与透明代理；`/v1/audio/transcriptions` 增加 multipart model 解析/授权/透明代理基础。真实 Embedding/ASR Provider 仍需最终环境验收。

### 多 Provider Route API

- `GET /admin/models/{model_id}/routes`
- `POST /admin/models/{model_id}/routes`
- `PUT /admin/model-routes/{route_id}`
- `DELETE /admin/model-routes/{route_id}`

Route 修改会事务化同步 legacy 主绑定，且禁止删除最后一条 Route、禁止停用/删除最后一条启用 Route。真正的 Failover/Circuit Breaker 继续由 ThinkWatch Core 执行，不在 Go Gateway 再造一套路由引擎。

### DeepSeek

Admin UI 已提供 DeepSeek preset：`https://api.deepseek.com/v1` + `openai-chat` + `DEEPSEEK_API_KEY`。代码不保存真实厂商密钥。真实 DeepSeek Key 与真实请求属于最终统一验收。

### SSO / RBAC

生产建议：`Campus IdP -> oauth2-proxy -> Campus AI Gateway trusted-header -> RBAC`。详情见 `docs/SSO_RBAC.md`。本地开发仍可使用 `ADMIN_AUTH_MODE=token`。

### 配额

Tenant 支持 `daily_token_limit / monthly_token_limit / monthly_request_limit / monthly_budget_micros`。Token 通过 Redis 预占并使用最终 usage 对账；没有可信 usage 时保留估算。详情见 `docs/QUOTAS.md`。

### 运维与压测

- `GET /healthz`：进程存活
- `GET /readyz`：PostgreSQL + Redis readiness
- `GET /metrics`：Prometheus text metrics
- `loadtest/k6-models.js`
- `loadtest/k6-chat.js`
- `docs/PERFORMANCE_TEST.md`
- `docs/PRODUCTION.md`

30,000 RPM ~= 500 RPS 只有在代表性生产拓扑记录实际 RPS、P50/P95/P99、错误率、CPU、内存、Redis、PostgreSQL、ThinkWatch 数据后才能标记通过。

### 最终统一验收入口

开发阶段不再要求逐项人工测试。最终一次性验收请按：`docs/FINAL_ACCEPTANCE.md`。

仍然必须在真实环境完成的项目：

1. DeepSeek 真实凭据和调用；
2. Qwen -> DeepSeek 真实 Failover；
3. ThinkWatch 最终 Provider/attempt telemetry bridge；
4. 学校真实 IdP 登录和各角色 RBAC；
5. Tenant quota 组合场景；
6. Multi-Gateway / PostgreSQL / Redis HA 故障切换；
7. Embedding / ASR 真实 Provider；
8. 30,000 RPM 性能验收。

相关新增文档：`docs/OPEN_SOURCE_REUSE.md`、`docs/FAILOVER_TEST.md`、`docs/SSO_RBAC.md`、`docs/QUOTAS.md`、`docs/PERFORMANCE_TEST.md`、`docs/PRODUCTION.md`、`docs/MODEL_SERVICES.md`、`docs/FINAL_ACCEPTANCE.md`。
