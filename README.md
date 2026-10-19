# Campus AI Gateway

校园大模型统一接入、模型路由、访问控制、限流与安全审计网关。

Campus AI Gateway 位于校园 AI 平台的统一模型入口层：对上提供 OpenAI-compatible API，对下通过网关自身的直连数据面接入不同模型供应商，并由 Go Gateway 统一承担租户、业务 API Key、模型权限、配额、管理员认证、RBAC、审计和运维能力。

> 当前状态（2026-10-18）：核心网关、Admin UI v3、管理员会话认证 v2、统一用户账户与开放注册、多 Provider 控制面、配额与审计已经进入 `main`；2026-10-17 完成一轮全项目逻辑审计与修复，封堵了权限提升链、修正了模型白名单语义、让租户停用真正生效，并加固了 trusted-header 边界。
>
> 已真实验证：Qwen 完整数据链路；Volcengine Ark / 豆包真实模型连通；管理员账号密码登录、会话轮换、多标签页并发刷新、RBAC、trusted-header 认证边界；2026-10-17 的修复项已用可控制的模拟上游在本地跑完 25/25 项行为断言。`go test ./...` 全绿、无 SKIP —— 依赖数据库的集成测试需要 PostgreSQL 17 与 Redis 7，CI（`.github/workflows/ci.yml`）以 service 方式提供，本地则用 `.\scripts\dev.ps1` 起容器；**没有数据库时这些测试会 SKIP，因此"CI 绿"必须与"CI 里真的有数据库"一起看**。
>
> 暂缓（冻结）：以下不在当前迭代范围内——完整业务数据面的双 Provider Failover 现场验收、学校真实 IdP、Multi-Gateway / PostgreSQL / Redis HA、Embedding / ASR 真实 Provider，以及 30,000 RPM 性能目标。当前迭代聚焦收敛：把限流边界收敛到租户、修正并发语义，并移除承诺了却不存在的能力。

---

## 1. 核心能力

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| OpenAI-compatible API | ✅ | `/v1/models`、`/v1/chat/completions`，并保留 Embedding / ASR 透明代理基础 |
| 业务 API Key | ✅ | `sk-campus-*`，数据库只保存前缀与 HMAC-SHA256 摘要 |
| Tenant 管理 | ✅ | 新增、编辑、启用 / 停用、配额与用量 |
| 模型 / IP Allowlist | ✅ | 每个业务 Key 独立授权 |
| RPM / TPM / 最大并发 | ✅ | Redis；Token 使用预占 + 最终 usage reconcile |
| Request ID / 请求审计 | ✅ | PostgreSQL `gateway_request_log` |
| SSE 透明代理 | ✅ | 流式响应不被 Gateway 缓冲 |
| Provider 管理 | ✅ | CRUD、状态、Base URL、凭据环境变量名 |
| 虚拟模型 / Route 管理 | ✅ | 一个虚拟模型可绑定多条 Provider 路线 |
| 直连数据面 | ✅ | 网关自己解析模型、注入凭据、流式透传、按 priority 故障切换（唯一数据面） |
| Qwen 真实调用 | ✅ | `campus-smart` 已完成真实端到端调用 |
| Volcengine Ark / 豆包 | ✅* | 真实模型测试已通过；完整业务数据面 Failover 仍待最终验收 |
| 多 Provider Failover 控制面 | ✅ | Route 优先级 / 权重 / 超时 |
| 逐次 attempt 遥测 | ✅ | 每次 Routing Attempt 落 `gateway_request_attempt`，请求详情展示完整 attempt chain（含被熔断跳过的线路） |
| Admin UI v3 | ✅ | React + TypeScript + Vite，构建产物嵌入 Go |
| 管理员会话认证 v2 | ✅ | 首次创建管理员、账号密码登录、Access + Refresh、设备会话 |
| 统一账户与开放注册 | ✅ | 公开注册创建 member、super_admin 授予管理角色、用户与权限管理 |
| RBAC | ✅ | `super_admin / gateway_admin / tenant_admin / auditor / operator` |
| trusted-header / 校园 SSO 边界 | ✅* | Gateway 侧完成；学校真实 IdP 待验收 |
| 管理审计 / 安全事件 | ✅ | 与业务请求日志分离 |
| Prometheus / 健康检查 | ✅ | `/healthz`、`/readyz`、`/metrics` |
| 30,000 RPM | 🧊 冻结 | k6 工具已完成；性能目标暂缓，等真实环境与压测证据 |

---

## 2. 架构

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
- **逐次 attempt 遥测**：每次 Routing Attempt 落 `gateway_request_attempt`，管理台的请求详情展示完整 attempt chain（含被跳过的线路）。

Admin UI 的「基础设施状态」第四盏灯显示为「数据面」，模型连通测试由网关直接向渠道发起请求，并在结果中回报实际应答的渠道。

直连模式下的路由表缓存 5 秒：在 Admin UI 保存渠道 / 模型 / 线路后，最长 5 秒内生效，不需要任何同步动作。

---

## 3. Admin UI 与管理员认证

启动 Gateway 后访问：

```text
http://127.0.0.1:8080/admin-ui
```

主要页面包括：

- 总览（KPI、基础设施状态、最近请求）
- 数据看板（流量 / Token / 延迟趋势、渠道状态）
- 渠道管理
- 模型与路由（线路 / 故障切换、模型连通测试）
- 租户 / API 密钥管理
- 用户（super_admin）
- 使用日志筛选、分页、CSV 与详情
- 审计日志与安全事件
- 个人资料、安全与访问（修改密码、登录设备、退出登录）

默认：

```text
ADMIN_AUTH_MODE=session
```

### 首次启动

数据库中没有管理员时，Admin UI 会显示“创建管理员账号”。

**项目不存在内置的默认账号或默认密码。** 首个 `super_admin` 完全由部署者自己创建，有两条路径：

1. **交互式（默认）**：打开 `http://127.0.0.1:8080/admin-ui`，首次会引导填写用户名 / 密码 / 确认密码。密码策略为 Argon2id、至少 10 位、不能等于用户名、字母 / 数字 / 符号至少包含两类。
2. **无人值守**：启动前设置以下两个变量，网关会在启动时自动创建首个管理员。仅当系统中**尚不存在 `super_admin`** 时生效；留空则回落到上面的交互式流程。

   ```text
   ADMIN_BOOTSTRAP_USERNAME=admin
   ADMIN_BOOTSTRAP_PASSWORD=<强密码>
   ```

首次 Web Setup 默认只允许真实客户端地址为 loopback 的请求。部署在 Nginx / oauth2-proxy 等反向代理后时，必须正确设置 `TRUSTED_PROXY_CIDRS`；Gateway 不会因为最后一跳代理来自 `127.0.0.1` 就把远程访问者当成本机。**因此在反向代理后部署时，远程访问者无法通过代理创建第一个管理员**：运维必须先在本机完成初始化，或改用上面的 `ADMIN_BOOTSTRAP_*` 无人值守方式。

创建首个管理员时使用数据库事务 + advisory lock，避免多实例并发初始化出多个首管理员。守卫条件是“是否已存在 super_admin”，而不是“账号表是否为空”。

**如果复用已有的数据库**（`DATABASE_URL` 指向一个已经初始化过的库），则不会再出现创建引导，必须直接使用该库中已有的账号登录——这些凭据不在仓库里，只有最初创建账号的人知道。

### 统一用户账户体系与开放注册

网关只有一种账号，管理员是拥有管理角色的普通账号：

```text
User
├── member          公开注册创建，无管理权限
├── auditor         只读观测
├── operator        运行状态与模型连通测试
├── tenant_admin    Phase 1 起不授予任何权限（全局形态已撤回，等同 member）
├── gateway_admin   渠道、模型与线路
└── super_admin      只能通过首次 Setup 创建，或由现有 super_admin 授予
```

- 首次部署没有 super_admin 时，**只**显示“创建管理员账号”，此时公开注册接口拒绝请求
  （`409 setup_required`），避免远程用户抢先注册导致系统无人可管理
- 首个 super_admin 创建后，`POST /auth/register` 仅在**显式设置**
  `PUBLIC_REGISTRATION_ENABLED=true` 时开放，且**只能**创建 `member`（后端固定，请求体
  携带 `roles` 等字段会被直接拒绝）
- 该开关默认 `false`：生产入口是校园 SSO，member 暂时没有数据面能力，默认开放匿名密码
  注册只会扩大滥用面；需要本地账号时由运维显式开启，账号体系其余部分不受影响
- 管理员角色只能由 super_admin 在「用户与权限」中授予；`/admin/users` 不在任何其它角色的
  允许列表里，gateway_admin 无法给自己提权
- 角色与 `member` 互斥：拥有任一管理角色时 `member` 自动移除，由后端 normalize
- 注册成功后自动登录（复用同一套 Access + Refresh Session），不需要再输一次密码
- 注册限流按来源 IP 计算（默认 5 次 / 小时），Redis 不可用时**拒绝**注册而不是放行
- 系统始终保留至少一个启用的 super_admin：停用或降级最后一个会被拒绝（`409 last_super_admin`）

注册与用户管理相关配置：

```text
PUBLIC_REGISTRATION_ENABLED=false
PUBLIC_REGISTER_MAX_ATTEMPTS=5
PUBLIC_REGISTER_WINDOW=1h
```

公开注册目前**不提供**邮箱验证、忘记密码、邀请码或管理员审批。

### 会话模型

管理员认证采用：

```text
用户名 + 密码
    ↓
Argon2id
    ↓
短期 Access Token（仅页面内存）
    +
HttpOnly Refresh Cookie
    ↓
服务端持久 Session
    ↓
RBAC
```

安全特性：

- 新密码使用 Argon2id；兼容 bcrypt 旧密码并可透明升级
- Access Token 不写入 localStorage / sessionStorage / IndexedDB
- Refresh Cookie 为 `HttpOnly` + `SameSite=Strict`
- Refresh Token 确定性轮换，默认 5 秒并发宽限期（`ADMIN_REFRESH_REPLAY_GRACE`）
- 多标签页同时刷新不会被误判为 Token 泄露
- 超出宽限期的旧 Refresh Token 重用会触发 replay 检测并撤销会话
- 修改密码会提升 `auth_version` 并使已有会话失效
- 支持登录设备列表与单设备撤销
- 登录限流覆盖 IP、IP + username、username 三个维度
- 登录 / Setup / Refresh 等认证响应使用 `Cache-Control: no-store`
- `ADMIN_SESSION_SECRET` 与业务 API Key HMAC 密钥分离

生产环境必须独立设置：

```text
ADMIN_SESSION_SECRET
```

建议使用至少 32 字节随机值，并且不要与 `API_KEY_HMAC_SECRET` 相同。

### trusted-header / 校园 SSO

生产可使用：

```text
Campus IdP
    ↓
oauth2-proxy / 等价 OIDC 组件
    ↓
trusted-header
    ↓
Campus AI Gateway RBAC
```

`ADMIN_AUTH_MODE=trusted-header` 时，Admin UI 不显示本地账号密码登录，而是直接使用可信代理注入的身份。

该模式下整个控制面的认证就是一个共享字符串，所以有两道额外约束：

- `ADMIN_PROXY_SECRET` 至少 32 个字符，不足会在启动时直接失败；
- `ADMIN_PROXY_CIDRS` 限定哪些对端地址可以尝试该认证。留空时保持旧行为（任何对端都可以试），启动日志会打印警告。

`ADMIN_AUTH_MODE=token` 仅保留为应急模式，Admin UI 不提供 Token 输入框。

---

## 4. Windows 本地开发

### 环境

- Go 1.24+
- Docker Desktop
- Node.js 22+（仅 Admin UI 构建阶段）

### 配置来源

网关的全部配置都通过**进程环境变量**注入，项目本身**不加载 `.env` 文件**（没有 dotenv 依赖，`scripts/` 与 Docker Compose 都不会读它）。仓库根目录的 `.env.example` 是**变量参考清单**，把它复制成 `.env` 不会被自动读取。

- Windows 本地开发：`scripts/setup.ps1` 会把 Provider 凭据写入 Windows **User 环境变量**；其余变量在启动前用 `$env:NAME = "value"` 设置。
- Docker Compose：在 `deploy/docker-compose.yml` 的 `environment` 段配置。
- 生产：使用 Secret Manager / KMS / Vault 注入，不要提交到仓库。

### 第一次初始化

```powershell
cd D:\AI\campus-ai-gateway
.\scripts\setup.ps1
```

只初始化、不启动：

```powershell
.\scripts\setup.ps1 -NoStart
```

### 日常启动

```powershell
.\scripts\dev.ps1
```

8080 已有旧 Gateway 时：

```powershell
.\scripts\dev.ps1 -ForceRestart
```

只启动、不自动打开浏览器：

```powershell
.\scripts\dev.ps1 -ForceRestart -NoBrowser
```

`dev.ps1` 固定使用直连数据面（`DATA_PLANE=direct`），只需要 PostgreSQL 与 Redis。

查看 / 停止：

```powershell
.\scripts\status.ps1
.\scripts\stop.ps1
```

### 数据库迁移

Schema 由独立命令管理，`dev.ps1` / `setup.ps1` 与 Docker Compose 栈（一次性 `migrate`
服务门禁）都调用同一实现：

```powershell
go run ./cmd/migrate -database-url "postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" -migrations .\migrations
```

- 按顺序应用 `migrations/` 下全部版本，并在 `schema_migrations` 中记录版本、文件名、
  校验值和应用时间；每个文件与自己的记录同一事务提交。
- 已记录文件的校验值变化会失败；没有 `schema_migrations` 的旧库会重放全部幂等迁移后
  建立记录；并发执行由 advisory lock 串行化。
- 迁移失败时命令非零退出，Compose 栈中的网关不会启动。

---

## 5. Provider 与模型

Provider 只保存：

- 名称
- Base URL
- 协议
- 凭据环境变量名
- 启用状态

**真实 Provider API Key 不写入 PostgreSQL，也不通过 Admin UI 保存。**

凭据环境变量名有命名空间约束：`ADMIN_*`、`API_KEY_*`、`DATABASE_*`、`REDIS_*`、`TRUSTED_*`、`HTTP_*` 等网关自用的变量族一律拒绝。原因是数据面在发请求时才用 `os.Getenv(credential_env)` 取值并发往渠道的 `base_url`——如果允许指向网关自己的密钥，那么"能编辑渠道"就等于"能读走任意密钥"。该约束由 `validateCredentialEnv` 强制，前缀列表而非固定名单，因此以后新增的 `ADMIN_*` 变量自动受保护。

协议字段：直连数据面只讲 OpenAI 格式，只接受 `openai-chat`；保存 `anthropic` / `gemini` 渠道会被拒绝。

内置模板包括：

| Provider | Base URL | Credential Env |
| --- | --- | --- |
| Qwen / 阿里云百炼 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `DASHSCOPE_API_KEY` |
| Volcengine Ark / 豆包 | `https://ark.cn-beijing.volces.com/api/v3` | `ARK_API_KEY` |
| DeepSeek | `https://api.deepseek.com/v1` | `DEEPSEEK_API_KEY` |
| OpenAI | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| GLM | `https://open.bigmodel.cn/api/paas/v4` | `ZHIPU_API_KEY` |
| Moonshot / Kimi | `https://api.moonshot.cn/v1` | `MOONSHOT_API_KEY` |

当前 Route 数据模型：

```text
virtual model alias
    ↓
gateway_model_route (1..N)
    ↓
provider + upstream model
    ↓
priority / weight / timeout / status
```

示例：

```text
campus-smart
    ├── priority 1 -> qwen
    └── priority 2 -> volcengine-ark
```

管理后台修改 Provider / Model / Route 后不需要任何同步动作，网关最长 5 秒内自己读到新线路。

### Volcengine Ark 本地开发

当前 PowerShell 需要先存在：

```powershell
$env:ARK_API_KEY
```

如果只保存在 Windows User / Machine 环境变量而当前终端尚未加载，可先把它导入当前 Process，再启动 Gateway。

不要把真实 Ark Key 提交到仓库。

---

## 6. Tenant 与业务 API Key

关系：

```text
Tenant
├── Key A
├── Key B
└── Key C
```

每个业务 Key 可以独立设置：

- 模型白名单
- IP / CIDR 白名单
- RPM
- TPM
- 最大并发
- 有效期
- 启用 / 停用

模型白名单有两种写法，语义是互斥的：

| `allowed_models` | 含义 |
|---|---|
| `[]` | 不允许调用任何对外模型 |
| `["*"]` | 允许全部对外模型，包括以后新增的 |
| `["a","b"]` | 只允许这两个 |

空数组**不是**"不限制"。之前代码把空数组当成全允许，而界面写的是"不勾选则不允许调用任何对外模型"，两者正好相反；迁移 `0011` 把存量的 `[]` 与 `null`（字段被整体省略时写入的形状）改写成 `["*"]`，所以已经存在的 Key 行为不变，但"全允许"从此是一个需要明确勾选的选项。

`/v1/` 下除 `/v1/models` 外的所有路径都必须带 `model` 字段，否则返回 `400 model_required`。这条规则是白名单而不是黑名单，新增的上游端点会自动受约束——否则一个没有 `model` 的请求会绕过模型白名单。

停用租户会立即让其下所有 Key 停止服务（`403 tenant_disabled`），不只是打一个标签。

完整 Key：

```text
sk-campus-...
```

完整 Key 只在创建时显示一次。数据库保存 `key_prefix` 与 HMAC-SHA256 `key_digest`，不保存完整明文 Key。

---

## 7. API 调用

### Models

```bash
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-campus-..."
```

### Chat Completions

```bash
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
```

业务请求会经过 Gateway 的 Tenant / Key / Allowlist / Quota / Audit 链路。

> Admin UI 中的“模型测试”用于验证对外模型的线路是否可用，它不是业务数据面请求，因此不会写入 `gateway_request_log`。它由网关直接向渠道发起请求，并在结果中回报实际应答的渠道。需要验证完整 Gateway 数据面时，请使用 `/v1/*` + `sk-campus-*` 业务 Key。

---

## 8. 请求与管理审计

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

管理员操作与安全事件分别写入独立审计表，避免和业务请求混在一起。

路由事实字段不会由 Gateway 猜测：每次 Routing Attempt 由网关自己落库，Provider / attempts 等字段以这份遥测为准。

---

## 9. Admin UI 构建

Admin UI 源码位于：

```text
web/admin/
```

检查与构建：

```powershell
npm --prefix .\web\admin install
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
```

生产运行时只使用构建后嵌入 Go 的静态资源，不需要单独运行 Node 服务。

---

## 10. 目录结构

```text
cmd/
├── server/                 Gateway HTTP 服务入口
├── keygen/                 业务 API Key 工具
└── migrate/                 数据库迁移命令入口

internal/
├── app/                    Server / Admin API / Auth / Embedded UI
│   ├── direct_proxy.go     直连数据面（唯一数据面）
│   └── adminui/dist/       Admin UI v3 构建产物
├── httpx/                  HTTP 公共能力
├── migrate/                迁移执行器（schema_migrations / 校验值 / advisory lock）
├── model/                  数据结构
├── ratelimit/              Redis 限流
├── security/               API Key HMAC / Password / JWT
└── storage/                PostgreSQL / Redis

migrations/
├── 0001_init.sql
├── 0002_management.sql
├── 0003_model_routes.sql
├── 0004_route_audit.sql
├── 0005_security_events.sql
├── 0006_quota.sql
├── 0007_admin_users.sql
├── 0008_admin_sessions.sql
├── 0009_admin_session_refresh_rotation.sql
├── 0010_analytics_indexes.sql
└── 0011_allowed_models_explicit.sql

web/
└── admin/                  React + TypeScript + Vite

deploy/
├── Dockerfile
├── docker-compose.yml
├── docker-compose.dev.yml
├── nginx.conf
├── nginx.production.example.conf
└── monitoring/             Prometheus 示例配置

loadtest/                   k6 压测脚本（k6-chat.js / k6-models.js）

scripts/
├── setup.ps1
├── dev.ps1
├── status.ps1
├── stop.ps1
├── verify-failover.ps1
└── e2e-admin-auth-v2.sh
```

---

## 11. 测试与验收

常规检查：

```powershell
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
go test -count=1 ./...
go vet ./...
go build ./...
```

管理员认证 E2E：

```bash
bash scripts/e2e-admin-auth-v2.sh
```

认证 v2 已覆盖：

- 首次本机 Setup
- 反向代理远程绕过防护
- 用户名 / 密码登录
- Argon2id / bcrypt 迁移
- Access / Refresh Session
- 多标签页并发 Refresh
- Replay grace / replay detection
- Session revoke
- 改密全会话失效
- CSRF
- 登录限流
- trusted-header
- 五角色 RBAC
- 浏览器凭据不落持久化存储

---

## 12. 安全与生产部署

本地开发默认值不能直接用于生产。

生产必须重点处理：

- `API_KEY_HMAC_SECRET`
- `ADMIN_SESSION_SECRET`
- PostgreSQL 密码
- Redis 安全配置
- Provider credentials
- 管理员强密码
- `ADMIN_COOKIE_SECURE=true`
- `TRUSTED_PROXY_CIDRS`
- HTTPS
- Secret Manager / KMS / Vault
- PostgreSQL / Redis / Gateway HA

使用 `ADMIN_AUTH_MODE=trusted-header` 时还需：

- `ADMIN_PROXY_SECRET` 不少于 32 个字符（不足时**启动直接失败**，不是警告）；
- `ADMIN_PROXY_CIDRS` 写明可信代理的地址段。留空会退化为"任何知道该字符串的对端都能以 super_admin 身份操作控制面"，启动日志会打印这条警告。

Provider API Key 应来自环境变量或 Secret Manager，不应进入代码仓库或业务数据库。

渠道的"凭据环境变量名"只允许指向第三方凭据，**不允许指向网关自用的变量**（`ADMIN_*` / `API_KEY_*` / `DATABASE_*` / `REDIS_*` 等）。数据面在请求时才用 `os.Getenv(名字)` 取值并发往渠道 `base_url`，因此一个能编辑渠道的人如果可以把名字指成网关自己的密钥，就等于可以读走该密钥；这条约束在保存渠道时就拒绝（`validateCredentialEnv`）。

管理员认证不依赖浏览器 localStorage。

---

## 13. 当前已知限制

> 标注 **🧊 冻结** 的条目不在当前迭代范围内（见顶部说明）。

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

## 14. Roadmap

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

### 下一阶段

- [ ] 管理员生命周期管理 / 密码恢复
- [ ] Secret Manager / KMS 接入
- [ ] 数据面自研补齐：真正的加权路由、按错误率的熔断、逐次 attempt 遥测

#### 冻结（暂不排期）

- [ ] 完整业务数据面 Qwen -> Ark Failover 验收
- [ ] 学校真实 IdP 接入验收
- [ ] Multi-Gateway / PostgreSQL / Redis HA
- [ ] Embedding / ASR 真实 Provider
- [ ] 30,000 RPM 正式性能验收

---

## 15. 项目定位

本仓库不是完整的“校园 AI 助手 / 智能体 / 知识库”产品。

它负责的是校园 AI 平台中更底层、也更关键的一层：

> **统一模型接入 + 访问控制 + 路由治理 + 配额 + 安全审计 + 管理控制面。**

当前项目已经进入：

> **可运行、可管理、可真实调用、可多 Provider 配置、可认证授权、可限流、可审计的校园大模型统一接入网关。**

接下来重点从“继续堆功能”转向真实生产环境的 **Failover、SSO、HA、Telemetry 与性能验收**。
