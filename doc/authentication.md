[← 返回 README](../README.md) · 管理员认证与账号体系

本页说明管理控制台的入口与认证模型：首次启动如何创建管理员、唯一账号体系与开放注册、Access + Refresh 的会话模型，以及 trusted-header / 校园 SSO 接入与五角色 RBAC 的权限边界。

---

## Admin UI 入口

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
| `gateway_admin` | `/admin/providers*`、`/admin/models*`、`/admin/model-routes*`、`/admin/model-test`、`/admin/model-test-options`、`/admin/status`、`/admin/status/trend` |
| `operator` | `GET /admin/status`、`GET /admin/status/trend`、`GET /admin/model-test-options`、`POST /admin/model-test`。连通测试是诊断而非编辑：operator 可以跑测试并从收窄的选项列表里选模型，但不能读写模型配置本身 |
| `auditor` | 仅 `GET`：`/admin/status`、`/admin/status/trend`、`/admin/request-logs*`、`/admin/admin-audit`、`/admin/security-events` |
| `tenant_admin` | 不授予任何权限。它本意是"某个租户自己的管理员"，但租户内部的成员模型还没做——以全局形态放开就会无范围地覆盖所有租户与 API Key。在正确的授权范围存在之前，这个角色**什么都不授予**（等同 member），而不是授予全部；持有该角色的账号可由 super_admin 重新分配 |
| `member` | 仅自助接口，无任何管理权限 |

`/admin/status/trend` 与 `/admin/status` 被显式分开列出，而不是用前缀规则覆盖：这样以后该路径下新增的端点必须自己说明授权理由。

---

相关：完整的环境变量清单见 [配置与凭据](configuration.md)；E2E 认证测试与验收清单见 [本地开发与验收](operations.md)。
