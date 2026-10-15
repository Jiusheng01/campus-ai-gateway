# 管理员认证与 RBAC

Campus AI Gateway 的管理控制台支持三种认证模式，由 `ADMIN_AUTH_MODE` 选择。
**默认模式是 `session`**：管理员在界面上用账号 + 密码登录，不再需要在浏览器里保存任何 Token。

| 模式 | 适用场景 | 说明 |
|---|---|---|
| `session`（默认） | 校园本地部署 | 账号密码登录，Access Token 只在内存，Refresh Token 走 HttpOnly Cookie |
| `token` | 本地开发 / 应急 | 单个 `ADMIN_TOKEN`，等价于 `super_admin`，用作 break-glass |
| `trusted-header` | 已接入校园 SSO | 由 oauth2-proxy 等反向认证组件注入身份头 |

---

## 1. 账号密码登录（`session`，默认）

### 1.1 首次启动

首次启动时数据库里没有任何管理员账号，界面会自动进入**创建管理员账号**页面，只需要填写：

- 用户名
- 显示名称（可选）
- 密码 / 确认密码

界面上**不会出现 ADMIN_TOKEN、初始化令牌等任何 Token 概念**，操作者不需要理解 Token。

首次创建只允许在**服务器本机**发起。从其他机器访问会收到：

```
setup_required_but_remote_forbidden
```

请在服务器上打开 `http://127.0.0.1:8080/admin-ui` 完成创建。

如需无人值守部署，可以改用环境变量预置初始账号（仅在账号不存在时生效）：

```text
ADMIN_BOOTSTRAP_USERNAME=admin
ADMIN_BOOTSTRAP_PASSWORD=<强密码>
```

### 1.2 日常登录

```
用户名 + 密码  ->  POST /admin/login
```

用户名**大小写不敏感**。用户名错误与密码错误返回完全相同的提示：

```
用户名或密码不正确
```

### 1.3 密码策略

- 长度 ≥ 10 个字符（按字符计，不是字节）
- 至少包含 字母 / 数字 / 符号 中的两类
- 不能与用户名相同

密码使用 **Argon2id** 存储（OWASP 推荐参数：内存 19 MiB、迭代 2、并行度 1、盐 16 字节、密钥 32 字节）。
旧版本写入的 **bcrypt**（`$2a$ / $2b$ / $2y$`）哈希仍然可以登录，并在登录成功后自动升级为 Argon2id，**不会导致其他设备掉线**。

### 1.4 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADMIN_AUTH_MODE` | `session` | 认证模式 |
| `ADMIN_SESSION_SECRET` | 空 | 管理员会话专用密钥（Access Token 签名 + Refresh 轮换派生），至少 32 字节；空则从 `API_KEY_HMAC_SECRET` 域分离派生并告警 |
| `ADMIN_ACCESS_TTL` | `15m` | Access Token 有效期 |
| `ADMIN_REFRESH_TTL` | `720h`（30 天） | 会话与 Refresh Cookie 有效期 |
| `ADMIN_REFRESH_REPLAY_GRACE` | `5s` | 刚被轮换掉的 Refresh Token 仍被视为「并发刷新」的宽限期，见 §1.5 |
| `ADMIN_LOGIN_MAX_ATTEMPTS` | `8` | 登录失败限流阈值（每个窗口）；用户名维度为 4 倍 |
| `ADMIN_LOGIN_WINDOW` | `15m` | 限流窗口长度 |
| `ADMIN_COOKIE_SECURE` | `false` | 走 HTTPS 时置 `true` |
| `ADMIN_SETUP_LOCAL_ONLY` | `true` | 首次初始化仅限本机，见 §1.6 |
| `TRUSTED_PROXY_CIDRS` | 空 | 可信反向代理网段，见 §1.6 |
| `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD` | 空 | 无人值守初始化（可选） |

`ADMIN_SESSION_SECRET` 必须与 `API_KEY_HMAC_SECRET` **取不同值**：

```text
openssl rand -base64 48
```

未配置时网关会打印 `ADMIN_SESSION_SECRET is not configured; derived development key is being used.`
该回退只为本地开发开箱即用，生产必须显式配置。

`ADMIN_TOKEN` 在 `session` 模式下**完全不生效**，避免残留的环境变量意外绕过登录系统。

### 1.5 令牌与会话模型

```text
POST /admin/login
  ├─ Access Token  : 短期（15 分钟），只保存在前端内存，随 Authorization: Bearer 发送
  └─ Refresh Token : 长期（30 天），保存在 HttpOnly Cookie campus_admin_refresh
                      Path=/admin/auth · SameSite=Strict · Secure（可选）
```

- 前端**不使用** localStorage / sessionStorage / IndexedDB 保存任何凭据。
- 数据库只保存 Refresh Token 的 SHA-256 摘要，明文只存在于浏览器 Cookie 中。
- 所有认证响应（`/admin/setup`、`/admin/login`、`/admin/auth/refresh`、`/admin/auth/logout`、
  `/admin/auth-config`、`/admin/whoami`）都带 `Cache-Control: no-store` 与 `Pragma: no-cache`。
- 账号的 `auth_version` 变化（改密码等）会让所有已签发凭证立即失效；会话自身的 `version` 变化只影响单个设备。
- 角色与账号状态**不写入令牌**，每次请求都从数据库读取，因此改角色 / 停用账号是即时生效的。

#### Refresh Token 轮换与并发语义

刷新时，下一个 Refresh Token 由 `HMAC(ADMIN_SESSION_SECRET, "refresh-rotation-v1" || sid || 当前密钥)`
**确定性派生**，因此同一个旧 Token 永远派生出同一个后继 Token。

数据库保留上一代摘要及其宽限截止时间：

```text
refresh_hash                  当前有效摘要
previous_refresh_hash         刚被轮换掉的摘要
previous_refresh_valid_until  该摘要仍可被接受的截止时间（now + ADMIN_REFRESH_REPLAY_GRACE）
```

判定规则：

| 情况 | 处理 |
|---|---|
| 提交的是当前摘要 | 正常轮换，返回后继 Token |
| 提交的是上一代摘要，且在宽限期内 | **良性并发刷新**（多标签页）：返回同一个后继 Token，`200`，不撤销任何会话，不清 Cookie |
| 竞争失败（CAS 未命中）但宽限期内的上一代摘要匹配 | 同上，按良性处理 |
| 提交的是更早的摘要，或已超出宽限期 | **判定为 replay**：撤销该账号全部会话，`401 refresh_replay_detected`，清 Cookie，写审计 |
| 会话已撤销 / 已过期 | `401`，清 Cookie |

因此：

- 多个浏览器标签页同时刷新**不会**被误判为凭据泄露，也不会互相清除对方的 Cookie；
- 真正被轮换掉的旧 Token 在宽限期之外再次出现才会触发全会话撤销；
- 只有 `session_revoked`、`session_expired`、`refresh_replay_detected` 等真实失败才会清除 Cookie。

#### 宽限期的边界与适用建议

宽限判定是 `now <= previous_refresh_valid_until`：等于截止时刻仍算宽限内，超出 1 纳秒即判 replay。
默认 `5s`，显式设置 `ADMIN_REFRESH_REPLAY_GRACE` 优先；取值为 `0`、负数或无法解析时回落到默认值
（不会以零宽限进入生产）。

这个窗口只做一件事：让**真正在竞争的并发刷新**收敛到同一个后继 Token，而不是被误判为凭据泄露。
需要分清它不做什么：

- 它**不能**防止当前有效 Refresh Token 被盗——被窃的当代 Token 与本取值无关，任何时候都可用；
- 它**不能**替代 `HttpOnly`、`Secure`、`SameSite`、确定性轮换、replay 检测和全会话撤销；
- 缩短宽限期只是缩小「上一代 Token 可被静默接受」的时间窗，**不等于消除 Refresh Token 重放/被盗风险**。

前端的单飞刷新（module 级共享 Promise）只避免**同一个 JS runtime** 内的重复请求；
真正的跨标签页、跨 runtime 并发安全，依靠的是服务端的确定性后继、`previous_refresh_hash`
与这里的宽限窗。两者互补，不能互相替代。

生产建议：默认 `5s` 即可；处于高延迟网络、特殊反向代理或跨区域管理控制台之后可谨慎调到 `10s`。
不建议恢复 `30s`（除非有明确实测需求），更不要设置为 `0s`——那会把正常的并发竞态
变成全会话撤销。

### 1.6 反向代理下的真实客户端 IP

登录限流按客户端 IP 计数。只有在**直连对端属于 `TRUSTED_PROXY_CIDRS`** 时才会采信 `X-Forwarded-For`，否则一律使用 TCP 对端地址。这样客户端无法通过伪造请求头绕过登录限流。

```text
TRUSTED_PROXY_CIDRS=127.0.0.1/32,10.0.0.0/8
```

**首次初始化也遵循同一规则。** `ADMIN_SETUP_LOCAL_ONLY=true` 时判断的是解析后的真实客户端地址，
而不是最后一跳：

| 直连对端 | 转发头 | 解析出的客户端 | 结果 |
|---|---|---|---|
| 本机 | 无 | 本机 | 允许 |
| 可信代理 | 有 | 本机 | 允许 |
| 可信代理 | 有 | 公网 / 内网其它地址 | 拒绝 `setup_required_but_remote_forbidden` |
| 本机 | 有，但该对端不在可信列表 | 无法确认 | 拒绝（fail closed） |
| 其它地址 | — | — | 拒绝 |

这堵住了「反向代理与本机同址，于是远程访问者被误判为 localhost」的绕过路径。

#### 登录限流的三个维度

| 维度 | Redis 键 | 阈值 |
|---|---|---|
| 客户端 IP | `gw:admin:login:ip:<hash>` | `ADMIN_LOGIN_MAX_ATTEMPTS` |
| IP + 用户名 | `gw:admin:login:pair:<hash>:<hash>` | `ADMIN_LOGIN_MAX_ATTEMPTS` |
| 用户名（跨 IP） | `gw:admin:login:user:<hash>` | `ADMIN_LOGIN_MAX_ATTEMPTS × 4` |

用户名先做 `lower(trim(username))` 归一化再哈希，与账号的大小写不敏感查找保持一致，
避免通过变换大小写放大预算。键名中只出现哈希值，不写入原始用户名或 IP。

登录成功只清除 **IP+用户名** 与 **用户名** 两个计数，**不清除 IP 级 spray 计数**：
否则持有任意一个合法低权限账号的攻击者可以靠不断成功登录来清空整个 IP 的防护预算。

Redis 不可用时限流 fail-open（不锁死管理员），但会记录 warning 日志，不会静默失败。

### 1.7 管理端接口

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/admin/auth-config` | 公开 | 登录页所需配置（不含任何凭据概念） |
| POST | `/admin/setup` | 本机 | 创建首个管理员并直接登录 |
| POST | `/admin/login` | 公开 | 账号密码登录 |
| POST | `/admin/auth/refresh` | Cookie | 轮换 Refresh Token 并签发新 Access Token |
| POST | `/admin/logout`、`/admin/auth/logout` | Cookie | 撤销当前会话并清理 Cookie |
| POST | `/admin/password` | Access Token | 修改自己的密码（成功后全会话失效） |
| GET | `/admin/sessions` | Access Token | 查看自己的登录设备 |
| POST | `/admin/sessions/{sid}/revoke` | Access Token | 撤销指定设备 |

> Cookie 的 `Path` 限定为 `/admin/auth`，浏览器只会把 Cookie 发到 `/admin/auth/*`。
> 因此前端调用 `/admin/auth/logout`；`/admin/logout` 作为 API 客户端的别名保留（命令行工具不受 Cookie Path 限制）。

### 1.8 错误语义

认证失败使用**类型化错误**判定 HTTP 状态码，不做错误文本匹配：

| 状态码 | 含义 |
|---|---|
| 401 | 未认证、凭证错误、会话过期 / 被撤销、账号不存在 |
| 403 | 角色不足、账号被停用、初始化不允许、CSRF 校验失败 |
| 429 | 登录尝试过于频繁 |
| 400 | 请求体非法、密码不符合策略 |
| 503 | 认证模式未正确配置；注册限流计数器不可用；注册创建的账号自动登录失败（提示直接登录） |

认证失败**永远不会**把凭证错误报成 503，避免客户端把「用户名或密码错误」误当成
「服务暂时不可用」而无限重试。上表后两种 503 都带有可执行的下一步（稍后重试 / 直接登录），
而不是要求重试同一请求。

---

## 2. 角色（RBAC）

- `super_admin`：全部管理权限
- `gateway_admin`：Provider / Model / Route / ThinkWatch 同步 / 模型测试
- `tenant_admin`：**Phase 1 起不授予任何权限**。未作用域化的全局形态可以触达全部租户与
  API Key，与已确认的「租户内 tenant_admin」设计矛盾，因此全局授权被收回：该名字仍作为
  存量数据被识别、显示，可由 super_admin 显式替换，但不再出现在可授予列表，持有者等价于
  `member`。作用域化成员关系属于后续阶段。
- `auditor`：状态、请求日志、管理审计、安全事件（只读）
- `operator`：运行状态和模型测试
- `member`：没有管理角色；只能操作自己的账号（`/admin/whoami`、改密码、登录设备）

`trusted-header` 模式下，代理未提供角色头、只提供未知角色、或提供已撤回的 `tenant_admin`
时，身份解析为 **`member`**（最小权限），不再兜底为 `auditor`——一个已认证但未映射的
账号不应该因此获得读取全部控制面的可见性。

以下接口属于**自助操作，不受 RBAC 限制**——任何已登录管理员都可以操作自己的账号：

```text
GET  /admin/whoami
POST /admin/password
GET  /admin/sessions
POST /admin/sessions/{sid}/revoke
POST /admin/logout
```

已认证用户可通过 `GET /admin/whoami` 查看解析后的身份与角色。

---

## 3. 兼容模式

### 3.1 本地开发 / 应急（`token`）

```text
ADMIN_AUTH_MODE=token
ADMIN_TOKEN=<long random secret>
```

该 Token 映射为 `super_admin`，用于在账号系统不可用时应急进入控制台。日常运行不建议启用。

**Admin UI 在 `token` 模式下不提供登录表单，也不提供任何 Token 输入框**：页面会明确提示当前为
应急 Token 模式，需要走管理 API 使用 `ADMIN_TOKEN`，或把 `ADMIN_AUTH_MODE` 切换为 `session`。

### 3.2 校园 SSO（`trusted-header`）

```text
Campus IdP
  -> oauth2-proxy（OIDC Session）
  -> Reverse Proxy
  -> Campus AI Gateway trusted-header auth
  -> RBAC
```

Gateway 配置：

```text
ADMIN_AUTH_MODE=trusted-header
ADMIN_PROXY_SECRET=<long random shared secret>
ADMIN_USER_HEADER=X-Forwarded-User
ADMIN_ROLES_HEADER=X-Forwarded-Groups
```

可信认证代理必须添加：

```text
X-Campus-Auth-Secret: <same ADMIN_PROXY_SECRET>
X-Forwarded-User: <stable campus account>
X-Forwarded-Groups: gateway_admin,auditor
```

角色头缺失、为空、或只包含未映射的组时，账号解析为 `member`（最小权限），不会被兜底
提升为任何管理角色；组名大小写与分隔符（逗号 / 分号 / 空格）不敏感，未知组名被丢弃。
身份头本身不是凭据：缺少或错误的 `X-Campus-Auth-Secret` 一律 401，与角色解析无关。

启用 `trusted-header` 后，不要把 Gateway 管理端口直接暴露给用户网络，只有可信反向代理应该能访问该端口。

#### Admin UI 在 `trusted-header` 模式下的行为

控制台**不再显示账号密码登录**，也不显示 Refresh Session：

```text
打开 /admin-ui
  -> GET /admin/auth-config            （login_enabled=false，auth_mode=trusted-header）
  -> GET /admin/whoami                 （由代理注入的身份头解析）
       ├─ 200  -> 直接进入控制台
       └─ 401/403 -> 显示「统一身份认证未完成或当前账号无管理权限」提示页
```

- 不调用 `/admin/auth/refresh`（该端点在此模式下返回 `503`）。
- 401 不会被当成「登录过期」，而是回到统一身份认证提示页。
- 「系统设置」中显示 **认证方式：学校统一身份认证**，不显示本地修改密码，也不显示本地登录设备列表
  （密码与设备由校园统一身份认证平台管理）。
- 顶栏不显示退出登录按钮。

---

## 4. 学校 IdP 最终接入

Gateway 本身不绑定具体学校 IdP。最终部署时需要在 oauth2-proxy 中配置：

- 学校 Issuer URL
- Client ID
- Client Secret
- Callback URL
- Group / Claim 映射

这些值属于真实部署环境，因此不会写入仓库。真实校园 SSO 登录属于**最终环境验收项**，不能仅靠 CI 宣称已经完成生产验证。

---

## 5. 数据库迁移

Schema 由独立命令 `cmd/migrate`（实现位于 `internal/migrate`）统一管理，本地开发与
Docker 栈（一次性 migrate 服务门禁）使用同一实现：

```powershell
go run ./cmd/migrate -database-url "$env:DATABASE_URL" -migrations .\migrations
```

`migrations/` 下全部文件按版本顺序应用并记录在 `schema_migrations`
（version / filename / checksum / applied_at），每个文件与自己的记录同一事务提交；
已记录文件的校验值变化会失败，没有该表的旧库会重放全部幂等迁移后建立记录。

管理员认证依赖两张表：

- `gateway_admin_user`（`migrations/0007_admin_users.sql`）
- `gateway_admin_session`（`migrations/0008_admin_sessions.sql`）

`0009_admin_session_refresh_rotation.sql` 为并发刷新安全性补充两列：

- `previous_refresh_hash`
- `previous_refresh_valid_until`

这些脚本都是**幂等**的：

- `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` + `ADD COLUMN IF NOT EXISTS`
- `0007` 还会为已存在的旧表补上 `auth_version` 列，并把大小写敏感的用户名唯一约束替换为 `lower(username)` 唯一索引
- 不会修改 Tenant / API Key / Provider / Model 等既有数据

若库中已存在仅大小写不同的重复用户名，`lower(username)` 唯一索引会创建失败，此时旧的约束仍然保留、数据未被破坏，请先清理重复账号再重新执行。

---

## 5.1 首个管理员的原子创建

`CreateFirstAdminUser` 在**单个事务**内先取 `pg_advisory_xact_lock(<固定常量>)`，再 `COUNT(*)`，
最后插入。仅依赖 `INSERT ... WHERE NOT EXISTS` 不足以保证原子性：在 READ COMMITTED 下两个并发事务
可能都看到空表并各自插入，从而留下两个超级管理员。

因此并发初始化时只有一个请求成功，另一个得到 `setup_not_allowed`。

---

## 6. 运维提示

- **忘记密码**：`session` 模式没有邮件找回。请由数据库管理员删除该账号后重新初始化，或直接用 `ADMIN_BOOTSTRAP_*` 预置新账号。
- **强制所有设备下线**：修改该账号的密码即可（会提升 `auth_version`）。
- **HTTPS 部署**：把 `ADMIN_COOKIE_SECURE` 置为 `true`。
