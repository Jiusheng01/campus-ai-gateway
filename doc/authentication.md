[← 返回 README](../README.md) · 管理员认证与账号体系

本页说明管理控制台的入口与认证模型：首次启动如何创建管理员、唯一账号体系与开放注册、Access + Refresh 的会话模型，以及 trusted-header / 校园 SSO 接入与五角色 RBAC 的权限边界。

---

## Admin UI 入口

启动 Gateway 后访问：

```text
http://127.0.0.1:8080/admin-ui
```

主要页面包括：

- 服务概览（KPI、基础设施状态、最近请求、模型诊断）
- 数据看板（流量 / Token / 延迟趋势、渠道状态）
- 上游渠道（接入、凭据来源、候选模型、连通诊断、运行时调用）
- 对外模型（已发布模型卡片、发布向导、模型诊断、停用影响预览）
- 路由策略（按对外模型分组的线路组，支持主备 / 加权 / 高级模式）
- 调用管理（API Key 与租户配额）
- 调用日志筛选、分页、CSV 与详情
- 审计日志与安全事件
- 用户（super_admin）
- 系统设置、个人资料、安全与访问（修改密码、登录设备、退出登录）

导航的「模型服务」组是「服务概览 / 上游渠道 / 对外模型 / 路由策略 / 调用管理 / 调用日志」：上游渠道管接入与凭据来源，对外模型管对外名称与其发布，路由策略管线路与故障切换，调用管理管 API Key 与租户配额。

默认：

```text
ADMIN_AUTH_MODE=session
```

---

## 首次启动

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

---

## 统一用户账户体系与开放注册

网关只有一种账号，管理员是拥有管理角色的普通账号：

```text
User
├── member          公开注册创建，无管理权限
├── auditor         只读观测
├── operator        运行状态与模型连通测试
├── tenant_admin    暂不授予任何权限（等同 member），待租户内部成员模型落地后启用
├── gateway_admin   渠道、模型与线路
└── super_admin      只能通过首次 Setup 创建，或由现有 super_admin 授予
```

- 首次部署没有 super_admin 时，**只**显示“创建管理员账号”，此时公开注册接口拒绝请求
  （`409 setup_required`），避免远程用户抢先注册导致系统无人可管理
- 首个 super_admin 创建后，`POST /auth/register` 仅在**显式设置**
  `PUBLIC_REGISTRATION_ENABLED=true` 时开放，且**只能**创建 `member`（后端固定，请求体
  携带 `roles` 等字段会被直接拒绝）
- 该开关默认 `false`：生产入口是校园 SSO，新注册的 member 默认没有租户、也就没有数据面能力，
  需要管理员分配租户后才能创建自己的 Key；默认开放匿名密码注册只会扩大滥用面，
  需要本地账号时由运维显式开启，账号体系其余部分不受影响
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

---

## 个人控制台（`/console`）

管理与个人两个工作区共用同一个构建、同一套会话与同一个数据面，区别只在导航、页面和 API 边界：

```text
http://127.0.0.1:8080/console
```

成员在这里查看账号与所属租户、管理自己的 API Key（创建 / 改名 / 停用 / 删除）、查看租户允许的模型、个人用量与调用日志，并修改密码、查看登录设备。管理控制台仍在 `/admin-ui`，两者互不替代。

### Account、Member 与 Tenant Membership

- **Account** 是认证主体，对应 `gateway_admin_user` 一行；管理员和成员都是 Account。
- **Member** 是不持有任何管理角色的 Account，不是另一张表；角色与 `member` 互斥，由后端 normalize。
- **Tenant Membership**：一个 Account 最多属于一个 Tenant（`gateway_admin_user.tenant_id`）。没有成员表，也不支持多租户或租户切换。
- 只有 `super_admin` 能在「用户」页分配或清除归属；Account 不能给自己分配。Account 仍拥有任何 API Key 时，更改归属会被拒绝（`409 user_owns_keys`，响应带 `owned_key_count`），避免 Key 悄悄花掉它主人已经离开的租户。
- 没有 Tenant 的 Account 仍能登录个人控制台，但会看到“等待分配租户”的空状态，并且不能创建 Key。
- 删除 Tenant 时，原本属于它的 Account 保留，归属被置空（`ON DELETE SET NULL`），回到未分配状态。

### 登录后的落地页

角色只决定**默认落地页**，不决定是否允许访问：

- 持有任一管理角色（`super_admin`、`gateway_admin`、`operator`、`auditor`）→ `/admin-ui/dashboard`
- 只持有 `member`、`tenant_admin`（该角色暂不授予权限，等同 member）或没有角色 → `/console/overview`

任何已登录 Account 都可以打开 `/console`；`/admin-ui` 需要管理角色，打开无权限的工作区会显示明确的 403 页面，而不是重定向循环。登录前访问的深链在登录后仍会保留；刷新 `/console/keys` 这类路径也会回到同一个页面。

### `/api/console/*` 的授权边界

- 复用同一套 Access Token + Refresh Cookie；`consoleMiddleware` 验证 Token 后要求非空的 account id，并拒绝已停用的账号。没有第二套凭据。
- **owner 来自会话，tenant 来自 Account 的 Tenant Membership**，两者都不读请求体或 URL。请求体里伪造 `owner_user_id` / `tenant_id` 不会生效；个人用量与日志按请求日志中的 owner 快照过滤，控制台自己的筛选条件只能收窄、不能放宽。
- 资源级 ownership：读取、修改、停用和删除只作用于当前 Account 名下的 Key；别人的 Key 返回 **404** 而不是 403，避免接口确认资源是否存在。
- Key 的模型范围在创建/编辑时受 Tenant 的 `allowed_models` 约束：显式列表必须是租户列表的子集；请求 `["*"]` 时，若租户是显式列表则展开为租户当前列表，若租户是 `["*"]` 则保持 `["*"]`；租户为 `[]` 时拒绝任何个人 Key 写入。数据面仍然只读 Key 自己的 `allowed_models`，不查租户列表（ADR-0020）。
- 每个 Account 的 Key 数量上限由 `PERSONAL_KEY_LIMIT` 控制（默认 10），在创建事务内校验，并发创建也不会超过。
- 个人控制台的写操作（创建 / 修改 / 停用 / 删除 Key）同样写入 `gateway_admin_audit_log`，actor 是发起操作 Account 的用户名。审计由 `consoleMiddleware` 统一记录，不读取请求体，因此新增的个人写接口天然被覆盖。
- 个人控制台的删除是**永久删除行**，不同于管理端的停用/吊销；调用日志通过 owner 快照保留归属，不会随 Key 删除而丢失。
- 个人接口按 ownership 授权，不按角色：管理员打开 `/console` 时也只能看到并操作自己名下的资源。

### 为什么 trusted-header 身份被拒绝

`ADMIN_AUTH_MODE=trusted-header`（以及应急的 `token` 模式）认证出的是用户名，没有稳定的 account id。个人控制台必须把 Key 绑定到可审计的 Account，因此 `/api/console/*` 对这类身份返回：

```text
403 console_identity_unavailable
```

这一版不给 trusted-header 增加账号映射或影子账号；需要使用个人控制台的成员应走 session 认证（本地账号 / 开放注册）。管理控制台在 trusted-header 下不受影响。

---

## 会话模型

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

---

## trusted-header / 校园 SSO

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

## RBAC 五角色与权限边界

`super_admin` 无限制；其余角色各自持有一份**显式路径允许列表**（`internal/app/admin_auth.go` 的 `adminPrincipal.can`）。这是 allow-list，也正是用户管理接口安全的原因：`/admin/users` 只落在 super_admin 分支里，任何其它角色——尤其是 `gateway_admin`——都无法给自己授予角色。

所有已登录账号都可以使用自助接口（不受 RBAC 影响）：`GET /admin/whoami`、改自己的密码、退出登录、列出并撤销自己的登录设备。对于一个纯 `member` 账号，这就是它能做的全部。

| 角色 | 可访问范围 |
| --- | --- |
| `super_admin` | 全部管理接口，包括用户与角色管理（`/admin/users`）、租户与 API Key 管理 |
| `gateway_admin` | `/admin/providers*`、`/admin/models*`、`/admin/model-catalog*`、`/admin/model-routes*`、`/admin/model-test`、`/admin/model-test-options`、`/admin/status`、`/admin/status/trend` |
| `operator` | `GET /admin/status`、`GET /admin/status/trend`、`GET /admin/model-test-options`、`POST /admin/model-test`。连通测试是诊断而非编辑：operator 可以跑测试并从收窄的选项列表里选模型，但不能读写模型配置本身 |
| `auditor` | 仅 `GET`：`/admin/status`、`/admin/status/trend`、`/admin/request-logs*`、`/admin/admin-audit`、`/admin/security-events` |
| `tenant_admin` | 不授予任何权限。它本意是"某个租户自己的管理员"，但租户内部的成员模型还没做——以全局形态放开就会无范围地覆盖所有租户与 API Key。在正确的授权范围存在之前，这个角色**什么都不授予**（等同 member），而不是授予全部；持有该角色的账号可由 super_admin 重新分配 |
| `member` | 仅自助接口，无任何管理权限 |

`/admin/status/trend` 与 `/admin/status` 被显式分开列出，而不是用前缀规则覆盖：这样以后该路径下新增的端点必须自己说明授权理由。

**编辑渠道意味着能令该渠道的 Credential 被解密**（ADR-0017）：内联来源下，`gateway_admin` 可以改写 `base_url` 把凭据发往自己控制的地址，也可以改写来源使数据面解密后使用它。读取接口仍不返回明文或密文，但这道写权限应当按「可接触密钥」授予，而不是按普通配置编辑处理。

---

相关：完整的环境变量清单见 [配置与凭据](configuration.md)；E2E 认证测试与验收清单见 [本地开发与验收](operations.md)。
