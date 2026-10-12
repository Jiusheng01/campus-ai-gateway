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
| `ADMIN_ACCESS_TTL` | `15m` | Access Token 有效期 |
| `ADMIN_REFRESH_TTL` | `720h`（30 天） | 会话与 Refresh Cookie 有效期 |
| `ADMIN_LOGIN_MAX_ATTEMPTS` | `8` | 登录失败限流阈值（每个窗口） |
| `ADMIN_LOGIN_WINDOW` | `15m` | 限流窗口长度 |
| `ADMIN_COOKIE_SECURE` | `false` | 走 HTTPS 时置 `true` |
| `ADMIN_SETUP_LOCAL_ONLY` | `true` | 首次初始化仅限本机 |
| `TRUSTED_PROXY_CIDRS` | 空 | 可信反向代理网段，见 §1.6 |
| `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD` | 空 | 无人值守初始化（可选） |

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
- 每次刷新都会**轮换** Refresh Token。若检测到已被轮换过的旧 Token 被再次使用，视为泄露，**撤销该账号的全部会话**。
- 账号的 `auth_version` 变化（改密码等）会让所有已签发凭证立即失效；会话自身的 `version` 变化只影响单个设备。
- 角色与账号状态**不写入令牌**，每次请求都从数据库读取，因此改角色 / 停用账号是即时生效的。

### 1.6 反向代理下的真实客户端 IP

登录限流按客户端 IP 计数。只有在**直连对端属于 `TRUSTED_PROXY_CIDRS`** 时才会采信 `X-Forwarded-For`，否则一律使用 TCP 对端地址。这样客户端无法通过伪造请求头绕过登录限流。

```text
TRUSTED_PROXY_CIDRS=127.0.0.1/32,10.0.0.0/8
```

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
| 503 | **仅**表示认证模式未正确配置（缺少必需设置） |

认证失败**永远不会**返回 503，避免客户端把「凭证错误」误当成「服务暂时不可用」而无限重试。

---

## 2. 角色（RBAC）

- `super_admin`：全部管理权限
- `gateway_admin`：Provider / Model / Route / ThinkWatch 同步 / 模型测试
- `tenant_admin`：Tenant / API Key 管理
- `auditor`：状态、请求日志、管理审计、安全事件
- `operator`：运行状态和模型测试

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

启用 `trusted-header` 后，不要把 Gateway 管理端口直接暴露给用户网络，只有可信反向代理应该能访问该端口。

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

管理员认证依赖两张表：

- `gateway_admin_user`（`migrations/0007_admin_users.sql`）
- `gateway_admin_session`（`migrations/0008_admin_sessions.sql`）

两个脚本都是**幂等**的：

- `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS`
- `0007` 还会为已存在的旧表补上 `auth_version` 列，并把大小写敏感的用户名唯一约束替换为 `lower(username)` 唯一索引
- 不会修改 Tenant / API Key / Provider / Model 等既有数据

若库中已存在仅大小写不同的重复用户名，`lower(username)` 唯一索引会创建失败，此时旧的约束仍然保留、数据未被破坏，请先清理重复账号再重新执行。

---

## 6. 运维提示

- **忘记密码**：`session` 模式没有邮件找回。请由数据库管理员删除该账号后重新初始化，或直接用 `ADMIN_BOOTSTRAP_*` 预置新账号。
- **强制所有设备下线**：修改该账号的密码即可（会提升 `auth_version`）。
- **HTTPS 部署**：把 `ADMIN_COOKIE_SECURE` 置为 `true`。
