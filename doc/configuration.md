[← 返回 README](../README.md) · 配置与凭据

本页说明网关的全部配置入口：配置从哪里来、每个环境变量做什么、Provider 与 Credential（渠道 / 凭据 / 协议 / Route）如何定义，以及生产部署必须处理的凭据与安全边界。变量清单汇总自仓库根目录的 `.env.example` 与 `internal/app/config.go`。

---

## 配置来源

网关的全部配置都通过**进程环境变量**注入，项目本身**不加载 `.env` 文件**（没有 dotenv 依赖，`scripts/` 与 Docker Compose 都不会读它）。仓库根目录的 `.env.example` 是**变量参考清单**，把它复制成 `.env` 不会被自动读取。

- Windows 本地开发：`scripts/setup.ps1` 只检查依赖并拉起开发栈，不再询问任何 Provider 密钥。其余变量在启动前用 `$env:NAME = "value"` 设置；Provider 密钥在控制台渠道表单里粘贴，或改用环境变量引用。
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
| `CREDENTIAL_ENCRYPTION_KEY` | 内联 Provider Credential 的 AES-256-GCM 加密密钥；任意长随机字符串会被哈希为 32 字节 | 生产必填 | 空（留空时从 `API_KEY_HMAC_SECRET` 派生域分离密钥并打警告，仅供本地开发） |

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

### 凭据环境变量（仅在渠道引用时使用）

这些变量只是常见模板的默认名称；渠道也可以直接在控制台粘贴 API Key，此时不需要设置任何环境变量。

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | Qwen / 阿里云百炼 | 仅当渠道以环境变量引用该名称时 | 空 |
| `ARK_API_KEY` | Volcengine Ark / 豆包 | 仅当渠道以环境变量引用该名称时 | 空 |
| `DEEPSEEK_API_KEY` | DeepSeek | 仅当渠道以环境变量引用该名称时 | 空 |
| `OPENAI_API_KEY` | OpenAI | 仅当渠道以环境变量引用该名称时 | 空 |
| `ZHIPU_API_KEY` | GLM | 仅当渠道以环境变量引用该名称时 | 空 |
| `MOONSHOT_API_KEY` | Moonshot / Kimi | 仅当渠道以环境变量引用该名称时 | 空 |

### 仅测试使用

| 变量 | 用途 | 必填 | 默认值 |
| --- | --- | --- | --- |
| `TEST_DATABASE_URL` | 集成测试使用的 PostgreSQL 连接串（默认值**随测试包不同**：`internal/storage` 与 `internal/migrate` 用 `campus_gateway_test`，`internal/app` 用 `campus_gateway_app_test`） | 仅测试 | 见左 |
| `TEST_REDIS_ADDR` | 集成测试使用的 Redis 地址 | 仅测试 | `127.0.0.1:6379` |

两个测试变量由 `.github/workflows/ci.yml` 在 CI 中以 service 形式提供；本地不设时依赖数据库的集成测试会 SKIP。

---

## Provider 与模型

Provider（控制台称「渠道」）保存：

- 名称
- Base URL
- 协议
- 对 Credential 的引用
- 启用状态

上游访问密钥不是 Provider 的字段，而是一条独立的 **Credential**。它有两种来源：

| 来源 | 保存内容 | 使用方式 |
| --- | --- | --- |
| `inline` | AES-256-GCM 密文（带 `v1:` 版本前缀） | 在控制台渠道表单里粘贴厂商 API Key；保存时立即加密，读取接口只返回 `source_type` / `configured` / `last4`，**永不回显密钥** |
| `env` | 环境变量名 | 数据面在发起 Routing Attempt 时用 `os.Getenv(名字)` 取值，并发往该渠道的 `base_url` |

编辑已有渠道时，key 字段留空表示**不改动**已保存的凭据，填入则覆盖。把渠道从内联凭据切换到环境变量引用（或反向）也在同一个表单里完成：勾选「改为引用环境变量」会出现环境变量名输入框；`env` 型渠道还会显示当前引用名。

环境变量来源仍有命名空间约束：`ADMIN_*`、`API_KEY_*`、`DATABASE_*`、`REDIS_*`、`TRUSTED_*`、`HTTP_*` 等网关自用的变量族一律拒绝。原因是数据面会把取到的值发往渠道的 `base_url`——如果允许指向网关自己的密钥，那么"能编辑渠道"就等于"能读走任意密钥"。该约束由 `validateCredentialEnv` 强制，前缀列表而非固定名单，因此以后新增的 `ADMIN_*` 变量自动受保护。`inline` 来源不经过这条检查：厂商密钥不是变量名，也不会被 `os.Getenv` 解析。

内联密文的密钥来自 `CREDENTIAL_ENCRYPTION_KEY`。未设置时，网关按 `ADMIN_SESSION_SECRET` 的既有模式从 `API_KEY_HMAC_SECRET` 派生域分离密钥并打启动警告——该回退只为本地开发免去额外配置。生产必须固定该密钥并妥善保管；一旦更换，已保存的内联 Credential 将无法解密。

协议字段：直连数据面只讲 OpenAI 格式，只接受 `openai-chat`；保存 `anthropic` / `gemini` 渠道会被拒绝。

Provider 模板包括（模板只预填 Base URL / 协议 / 环境变量名；凭据来源仍由表单里的开关决定）：

| Provider | Base URL | 模板环境变量名 |
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

### 本地开发的凭据

本地不需要预先准备厂商密钥。启动开发栈后，在控制台「新增渠道」里选择模板、粘贴厂商 API Key 即可；密钥加密入库，之后只显示尾号和"已配置"。

如果更愿意沿用环境变量，在同一个表单里勾选「改为引用环境变量」，填入模板给出的变量名，再在启动网关的终端里设置它：

```powershell
$env:ARK_API_KEY = "…"
```

不要把真实 Key 提交到仓库。

---

## 安全与生产部署

本地开发默认值不能直接用于生产。

生产必须重点处理：

- `API_KEY_HMAC_SECRET`
- `ADMIN_SESSION_SECRET`
- `CREDENTIAL_ENCRYPTION_KEY`
- PostgreSQL 密码与备份
- Redis 安全配置
- Provider Credential（内联密文或环境变量引用）
- 管理员强密码
- `ADMIN_COOKIE_SECURE=true`
- `TRUSTED_PROXY_CIDRS`
- HTTPS
- Secret Manager / KMS / Vault
- PostgreSQL / Redis / Gateway HA

使用 `ADMIN_AUTH_MODE=trusted-header` 时还需：

- `ADMIN_PROXY_SECRET` 不少于 32 个字符（不足时**启动直接失败**，不是警告）；
- `ADMIN_PROXY_CIDRS` 写明可信代理的地址段。留空会退化为"任何知道该字符串的对端都能以 super_admin 身份操作控制面"，启动日志会打印这条警告。

### Credential 的边界

- **内联 Credential** 以 AES-256-GCM 密文保存在 `gateway_credential`，密钥来自 `CREDENTIAL_ENCRYPTION_KEY`。数据库转储或备份泄露不等于厂商密钥泄露；但**加密密钥必须与数据库分开保管**（Secret Manager / KMS / Vault），同时拿到两者仍可解密。
- **只写不读**：读取接口只返回来源（`inline` / `env`）、是否已配置和尾 4 位，永不返回明文或密文；写接口接受 `api_key`（内联）或 `credential_env`（环境变量名），两者互斥。
- **环境变量来源**的引用名只允许指向第三方凭据，**不允许指向网关自用的变量**（`ADMIN_*` / `API_KEY_*` / `DATABASE_*` / `REDIS_*` 等）。数据面在请求时才用 `os.Getenv(名字)` 取值并发往渠道 `base_url`，因此一个能编辑渠道的人如果可以把名字指成网关自己的密钥，就等于可以读走该密钥；这条约束在保存渠道时就拒绝（`validateCredentialEnv`）。
- **编辑渠道现在等于能令该渠道的 Credential 被解密**（ADR-0017）：内联来源下，管理员可以把它改成环境变量引用、或把 `base_url` 指向自己控制的地址，从而让凭据外发。授予 `gateway_admin` 渠道编辑权时，应按"可接触密钥"处理。
- **fail-closed**：环境变量为空或密文无法解密时，请求按既有 `missingCredentialError` 语义失败，不会以空密钥或旧密钥继续发往上游。
- 加密密钥变更、数据库恢复与迁移时，应同时验证现有内联 Credential 仍可解密；否则这些渠道会在数据面 fail-closed。

管理员认证不依赖浏览器 localStorage。

---

## 配额与限流：不在环境变量里

这两样都**按 Tenant 在管理台设置**，不是启动参数：

- **Quota（日 / 月用量上限）**：`daily_token_limit`、`monthly_token_limit`、`monthly_request_limit`。定义在 Tenant 上，同租户的**所有 API Key 共享**一份；**0 表示不限**。用量以 `gateway_request_log` 为唯一权威来源，Redis 里的计数只是它可重建的投影。
- **Rate Limit（每分钟上限与并发）**：`rpm_limit`、`tpm_limit`、`max_concurrency`。同样定义在 Tenant 上，由 Redis 在请求时刻判定，没有持久真相。

两者正交：Quota 管"这个月能用多少"，Rate Limit 管"此刻能打多快"。

> 每个 Key 上还有 `route_group`，以及 Tenant 上的 `monthly_budget_micros`：这两个字段仍在库表与 API 中，但**数据面不读取**，所以管理台已不再提供编辑。详见[已知限制](design.md)。
