[← 返回 README](../README.md) · 配置与凭据

本页说明网关的全部配置入口：配置从哪里来、每个环境变量做什么、Provider 与模型（渠道 / 协议 / Route）如何定义，以及生产部署必须处理的凭据与安全边界。变量清单汇总自仓库根目录的 `.env.example` 与 `internal/app/config.go`。

---

## 配置来源

网关的全部配置都通过**进程环境变量**注入，项目本身**不加载 `.env` 文件**（没有 dotenv 依赖，`scripts/` 与 Docker Compose 都不会读它）。仓库根目录的 `.env.example` 是**变量参考清单**，把它复制成 `.env` 不会被自动读取。

- Windows 本地开发：`scripts/setup.ps1` 会把 Provider 凭据写入 Windows **User 环境变量**；其余变量在启动前用 `$env:NAME = "value"` 设置。
- Docker Compose：在 `deploy/docker-compose.yml` 的 `environment` 段配置。
- 生产：使用 Secret Manager / KMS / Vault 注入，不要提交到仓库。

---

## 环境变量总表

### 基础运行

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `HTTP_ADDR` | 网关 HTTP 监听地址 | 否 | `:8080` |
| `DATABASE_URL` | PostgreSQL 连接串 | 是 | 无（本地由 `scripts/setup.ps1` 写入） |
| `REDIS_ADDR` | Redis 地址 | 否 | 代码默认 `redis:6379`；`.env.example` 示例 `localhost:6379` |
| `REDIS_PASSWORD` | Redis 密码 | 否 | 空 |
| `REDIS_DB` | Redis DB 序号 | 否 | `0` |
| `API_KEY_HMAC_SECRET` | 业务 API Key 的 HMAC-SHA256 密钥 | 是 | 无 |
| `DATA_PLANE` | 数据面选择。只接受 `direct`；`auto` 与留空同样解析为 `direct`（`thinkwatch` / `shadow` 已随该数据面一起移除，写了会启动失败） | 否 | `direct` |

### 管理员认证与会话

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `ADMIN_AUTH_MODE` | 认证模式：`session` / `trusted-header` / `token` | 否 | `session` |
| `ADMIN_SESSION_SECRET` | 管理员会话密码学专用密钥（Access 签名与 Refresh 轮换）；不得与 `API_KEY_HMAC_SECRET` 相同 | 生产必填 | 空（留空时从 `API_KEY_HMAC_SECRET` 派生域分离密钥并打警告，仅供本地开发） |
| `ADMIN_ACCESS_TTL` | Access Token 有效期 | 否 | `15m` |
| `ADMIN_REFRESH_TTL` | Refresh Session 绝对有效期 | 否 | `720h` |
| `ADMIN_REFRESH_REPLAY_GRACE` | 刚轮换过的 Refresh Token 仍被当作良性并发的宽限期；超出后重用按 replay 处理 | 否 | `5s`（不要设 0） |
| `ADMIN_LOGIN_MAX_ATTEMPTS` | 登录限流预算；按用户名维度为 4 倍该值 | 否 | `8` |
| `ADMIN_LOGIN_WINDOW` | 登录限流窗口 | 否 | `15m` |
| `ADMIN_SETUP_LOCAL_ONLY` | 首次 Setup 是否只允许 loopback 客户端 | 否 | `true` |
| `ADMIN_COOKIE_SECURE` | 给 Refresh Cookie 加 `Secure`；需要 HTTPS | 否（生产应 `true`） | `false` |
| `ADMIN_PUBLIC_ORIGIN` | 运维实际访问控制台的 origin，例如 `https://gateway.example.edu`；仅用于 `/admin/auth/refresh` 与 `/admin/auth/logout` 的同源校验 | 否 | 空 |
| `TRUSTED_PROXY_CIDRS` | 信任哪些代理的 `X-Forwarded-For`；不在列表内的对端一律忽略该头 | 否（反向代理后部署必填） | 空 |
| `ADMIN_BOOTSTRAP_USERNAME` | 无人值守创建首个管理员的用户名 | 否 | 空（回落到交互式 Setup） |
| `ADMIN_BOOTSTRAP_PASSWORD` | 无人值守创建首个管理员的密码 | 否 | 空（回落到交互式 Setup） |

### 开放注册

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `PUBLIC_REGISTRATION_ENABLED` | 是否开放公开注册（只能创建 `member`） | 否 | `false` |
| `PUBLIC_REGISTER_MAX_ATTEMPTS` | 注册限流，按来源 IP 计 | 否 | `5` |
| `PUBLIC_REGISTER_WINDOW` | 注册限流窗口 | 否 | `1h` |

### trusted-header / 应急模式

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `ADMIN_PROXY_SECRET` | trusted-header 模式下整个控制面的共享字符串，**至少 32 字符**，不足启动直接失败 | `ADMIN_AUTH_MODE=trusted-header` 时必填 | 空 |
| `ADMIN_PROXY_CIDRS` | 允许尝试 trusted-header 认证的对端地址段，逗号分隔；留空表示任何对端都可试（启动打印警告） | 强烈建议 | 空 |
| `ADMIN_USER_HEADER` | 注入用户名的请求头 | 否 | `X-Forwarded-User` |
| `ADMIN_ROLES_HEADER` | 注入角色的请求头 | 否 | `X-Forwarded-Groups` |
| `ADMIN_TOKEN` | `ADMIN_AUTH_MODE=token` 的 break-glass Token，Admin UI 不提供输入框 | `token` 模式时必填 | 无 |

### Provider 凭据

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | Qwen / 阿里云百炼 | 使用该渠道时必填 | 空 |
| `ARK_API_KEY` | Volcengine Ark / 豆包 | 使用该渠道时必填 | 空 |
| `DEEPSEEK_API_KEY` | DeepSeek | 使用该渠道时必填 | 空 |
| `OPENAI_API_KEY` | OpenAI | 使用该渠道时必填 | 空 |
| `ZHIPU_API_KEY` | GLM | 使用该渠道时必填 | 空 |
| `MOONSHOT_API_KEY` | Moonshot / Kimi | 使用该渠道时必填 | 空 |

### 仅测试使用

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `TEST_DATABASE_URL` | 集成测试使用的 PostgreSQL 连接串（默认值**随测试包不同**：`internal/storage` 与 `internal/migrate` 用 `campus_gateway_test`，`internal/app` 用 `campus_gateway_app_test`） | 仅测试 | 见左 |
| `TEST_REDIS_ADDR` | 集成测试使用的 Redis 地址 | 仅测试 | `127.0.0.1:6379` |

两个测试变量由 `.github/workflows/ci.yml` 在 CI 中以 service 形式提供；本地不设时依赖数据库的集成测试会 SKIP。

---

## Provider 与模型

Provider 只保存：

- 名称
- Base URL
- 协议
- 凭据环境变量名
- 启用状态

**真实 Provider API Key 不写入 PostgreSQL，也不通过 Admin UI 保存。**

凭据环境变量名有命名空间约束：`ADMIN_*`、`API_KEY_*`、`DATABASE_*`、`REDIS_*`、`TRUSTED_*`、`HTTP_*` 等网关自用的变量族一律拒绝。原因是数据面在发请求时才用 `os.Getenv(credential_env)` 取值并发往渠道的 `base_url`——如果允许指向网关自己的密钥，那么"能编辑渠道"就等于"能读走任意密钥"。该约束由 `validateCredentialEnv` 强制，前缀列表而非固定名单，因此以后新增的 `ADMIN_*` 变量自动受保护。

协议字段：直连数据面只讲 OpenAI 格式，只接受 `openai-chat`；保存 `anthropic` / `gemini` 渠道会被拒绝。

Provider 模板包括：

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

## 安全与生产部署

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

## 配额与限流：不在环境变量里

这两样都**按 Tenant 在管理台设置**，不是启动参数：

- **Quota（日 / 月用量上限）**：`daily_token_limit`、`monthly_token_limit`、`monthly_request_limit`。定义在 Tenant 上，同租户的**所有 API Key 共享**一份；**0 表示不限**。用量以 `gateway_request_log` 为唯一权威来源，Redis 里的计数只是它可重建的投影。
- **Rate Limit（每分钟上限与并发）**：`rpm_limit`、`tpm_limit`、`max_concurrency`。同样定义在 Tenant 上，由 Redis 在请求时刻判定，没有持久真相。

两者正交：Quota 管"这个月能用多少"，Rate Limit 管"此刻能打多快"。

> 每个 Key 上还有 `route_group`，以及 Tenant 上的 `monthly_budget_micros`：这两个字段仍在库表与 API 中，但**数据面不读取**，所以管理台已不再提供编辑。详见[已知限制](design.md)。
