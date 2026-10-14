# 统一用户账户体系与开放注册

本文档描述 Campus AI Gateway 的账号模型。它只覆盖**控制面账号**，
即谁能进入管理后台；业务侧的 `Tenant` / `API Key` 是另一套模型，本文不涉及。

---

## 1. 一种账号，六种角色

网关只有一种账号。管理员不是一个独立的账号类型，而是普通账号拥有了管理角色。

```text
User
├── member           公开注册创建，无网关管理权限
├── auditor          只读观测：请求日志、管理审计、安全事件
├── operator         总览 + 模型连通测试
├── tenant_admin     租户与 API Key
├── gateway_admin    供应商、模型与线路、ThinkWatch 同步
└── super_admin      全部权限，含用户与权限管理
```

角色语义与 `ADMIN_AUTH_MODE=trusted-header` 接收的校园 SSO 角色一致，这样将来
IdP 接入时不需要重新映射。

### member 与管理员角色互斥

`member` 表示“没有管理角色”，不是一个可以叠加的权限。后端 `NormalizeAccountRoles`
在任何写入角色的路径上都会执行同一条规则：

- 去掉未知角色
- 去重
- 一旦出现任一管理角色，移除 `member`
- 结果为空时变成 `["member"]`

因此 `["member", "gateway_admin"]` 不可能被存储，也不会在后续被按不同的解释读出。

---

## 2. 首次 Setup 与公开注册的分界

| 系统状态 | `/admin/setup` | `POST /auth/register` |
|---|---|---|
| 没有任何 super_admin | 允许（并受本机 / 可信代理限制） | 拒绝，`409 setup_required` |
| 已存在 super_admin | 拒绝，`403 setup_not_allowed` | 允许，创建 `member` |
| `PUBLIC_REGISTRATION_ENABLED=false` | 正常 | 拒绝，`403 registration_disabled` |

关键在于**首个管理员的判定条件是“是否存在 super_admin”，不是“账号表是否为空”**。
在此之前公开注册如果抢先创建了一个 member，部署就会停在“没有任何人能管理它，而
setup 又因为已经有账号而被拒绝”的状态。

因此：

- 两个入口共用同一种保护机制但守卫条件不同：`CreateFirstAdminUser` 在事务内取
  advisory lock 后统计 super_admin 数量；`CreateUser` 只做普通 INSERT 并处理用户名冲突。
- 普通注册**不能**走 first-admin 路径，两者在代码里是完全独立的语句。

---

## 3. 注册接口

```http
POST /auth/register
Content-Type: application/json

{
  "username": "zhangsan",
  "display_name": "张三",
  "password": "...",
  "confirm_password": "..."
}
```

- 请求体使用 `DisallowUnknownFields` 严格解码。携带 `roles` / `status` / `is_admin` /
  `auth_version` 等字段的请求会被拒绝为 `400 invalid_request`，而不是被悄悄忽略——
  这样提权尝试会出现在审计里，而不是看不见。
- 角色由后端固定为 `["member"]`，不是接口参数。
- 用户名最多 64 rune，按 `lower(username)` 大小写不敏感唯一；重复返回
  `409 username_taken`。
- 密码沿用 Session Auth v2 策略：Argon2id、最少 10 位、最多 200 位、不能等于用户名、
  至少包含字母 / 数字 / 符号中的两类。
- 注册成功后**自动登录**：创建 Session，返回 Access Token 并写入 HttpOnly Refresh
  Cookie。`login_method` 记为 `register`，审计里可以和行为上的登录区分开。
- 注册成功写入一条 `account_registered` 安全事件，内容包含 user_id、username 与
  来源 IP；**不记录**密码、密码哈希或任何 Token。

### 注册限流

```text
PUBLIC_REGISTER_MAX_ATTEMPTS=5
PUBLIC_REGISTER_WINDOW=1h
```

- 按来源 IP 计数，**每次尝试**都计一次（包括用户名重复的失败）。如果只统计成功，
  脚本可以用一个已占用的用户名全速探测。
- Redis 不可用时**拒绝**注册（`503 registration_unavailable`）。这与登录限流的
  fail-open 策略相反：如果计数器拿不到，无限注册比暂时不能注册更糟。

---

## 4. 权限管理（super_admin）

```http
GET   /admin/users                  # 列表 + 统计
PATCH /admin/users/{id}             # display_name / roles / status
```

- 只有 super_admin 能访问；网关的 RBAC 允许列表里没有任何其它角色能碰到
  `/admin/users`。这一点比 UI 隐藏菜单更重要：gateway_admin 若能访问用户管理，
  就能把自己提成 super_admin。
- 响应中**不出现** `password_hash` / `refresh_hash` / `auth_version`。
- `roles` 会经过 normalize；`status` 只接受 `1`（启用）或 `2`（停用）。
- 改角色、停用 / 启用都提升 `auth_version` 并撤销该账号的所有 Session，因此被调整的
  用户必须重新登录。改显示名称不提升版本，也不会踢人下线。
- 没有任何硬删除：账号只做启用 / 停用，审计、日志与关联关系都保留。

### 审计动作

| 动作 | 含义 |
|---|---|
| `account_registered` | 注册成功 |
| `registration_rate_limited` | 注册被限流 |
| `user.role.update` | 变更角色 |
| `user.disable` / `user.enable` | 停用 / 启用 |
| `user.profile.update` | 修改显示名称 |
| `user.update_failed` | 更新被拒绝（含 `last_super_admin`） |

---

## 5. 最后一个 super_admin 保护

系统必须始终保留至少一个**启用的** super_admin。停用一个已禁用的 super_admin
不算恢复保护，因为它无法再登录管理任何东西。

该不变量在**写入事务内**用 advisory lock 校验，而不是在 service 层先查后写：
两个并发的降级请求会各自看到对方的 super_admin 然后都执行成功，最终系统无人可管。
违反时返回 `409 last_super_admin`。

有多个 super_admin 时，允许其中一个降级或停用 —— 这正是交接管理权限的方式。

---

## 6. 账户变更语义

一个 PATCH 是**一个逻辑变更**。`UpdateAdminUserAccount` 在单个事务里完成
`pg_advisory_xact_lock` → `SELECT ... FOR UPDATE` → 计算最终状态 → 一次 last-super-admin
判定 → 一次 UPDATE，并返回真实的 `AdminUserChangeSet`。

### auth_version

`auth_version` 是凭据失效的开关：它一变，所有在它之前签发的 Access Token 与 Session
立即失效。

| 变更 | auth_version | 会话 |
|---|---|---|
| 仅 display_name | **+0** | 保留 |
| 仅 roles | **+1** | 撤销 |
| 仅 status | **+1** | 撤销 |
| roles + status | **+1** | 撤销一次 |
| roles + display_name | **+1** | 撤销一次 |
| status + display_name | **+1** | 撤销一次 |
| 三者同时 | **+1** | 撤销一次 |
| **no-op** | **+0** | 保留 |

显示名称是标签，不影响鉴权，所以改它不踢人下线。权限与状态决定 Access Token 携带的
授权上下文，所以它们一变就必须让旧 Token 失效。

**一个 PATCH 最多 +1**，无论它改了几个字段。

### no-op

如果三个字段都没有实际变化：

- 不执行 UPDATE
- 不 bump `auth_version`
- 不修改 `updated_at`
- 不撤销任何 Session
- 不写任何业务审计

「请求里有 roles 字段」和「roles 变了」是两件事。只改显示名称的请求里 roles 字段
根本没被发送（前端只发变化字段），即使发送了相同值，后端也比较前后状态后按 no-op 处理。

角色比较走 canonical 形式，因此「同样的角色换个顺序」不算变化。

### 角色校验

管理员 PATCH 携带未知角色返回 `400 invalid_request`，不静默丢弃 ——
`["gateway_admin","god_mode"]` 被拒绝，而不是变成 `["gateway_admin"]`。

之后 `NormalizeAccountRoles` 负责去重、`member` 与管理角色互斥、固定排序。
公开注册仍然完全不接受 roles 字段。

### 事务原子性

一个被拒绝的 PATCH 不会半途生效。例如同时改 roles + status + display_name 而违反
last-super-admin 时，**所有字段都保持不变**，不会出现「显示名称改了但角色被拒绝」。

### 撤销原因

每个逻辑 mutation 只调用一次 `RevokeSessionsForUser`，原因按实际变化记录：

```text
仅 roles                → role_changed
status → disabled       → account_disabled
status → enabled        → account_status_changed
roles + status          → account_security_changed
```

撤销是 best effort：事务已经 bump 了 `auth_version`，旧 Token 在下一次请求就会失效，
因此清理 session 行失败不会回滚已经提交的权限变更。

### 审计

审计按真实 ChangeSet 记录，多字段 PATCH 写多条语义事件：

```text
roles + disable  →  user.role.update + user.disable
roles + profile  →  user.role.update + user.profile.update
三字段           →  user.role.update + user.disable/enable + user.profile.update
no-op            →  不写任何业务审计
```

---

## 7. 注册限流

限流是**一次原子 Redis 操作**：`ConsumeRegistration` 在同一个 Lua 脚本里完成
INCR、TTL 与判定。

分两步（先问「可以吗」再「记一次」）会留下 TOCTOU 窗口：上限 5 时，同时到达的 50 个
请求可以全部读到计数 4、全部通过、再各自自增，一次突发就绕过了整个 anti-abuse 限制。

- 判定是 `current <= maxAttempts`，所以上限 5 恰好放行 5 个。
- **窗口从第一个请求起算且不延长**：只在 `current == 1` 时设置过期，反复重试无法把
  重置时间推远。
- `PUBLIC_REGISTER_MAX_ATTEMPTS=0` 关闭限流，此时完全不访问 Redis。
- **Redis 不可用时拒绝注册**（`503 registration_unavailable`）。这与登录限流的
  fail-open 相反：计数器拿不到时无限注册比暂时不能注册更糟。

消耗时机在基础校验之后、创建账号之前，因此：

```text
不计费：malformed JSON / 弱密码 / 确认不一致 / 超长用户名 / 携带安全字段
计费：  用户名重复（否则可用已知存在的名字免费探测接口）
```

---

## 8. 普通用户看到什么

member 登录后不会进入总览：它没有 `/admin/status` 权限，进入只会到处 403。
它看到的是「我的账号」页：

- 账号名、角色、状态
- 一段说明：当前没有网关管理权限，如需权限请联系超级管理员
- 修改密码、登录设备、撤销设备、退出登录（与管理员完全同一套组件）

member 唯一被允许的后端能力是自助端点：`/admin/whoami`、`/admin/password`、
`/admin/sessions*`、退出登录。其它所有管理接口一律 403。

---

## 9. 与业务 Tenant / API Key 的关系

**本轮没有把账号绑定到 API Key。** 现有模型仍是：

```text
Tenant ──> API Key ──> 业务数据面
```

member 第一版只是控制面账号，不拥有任何 API Key、配额或用量。要做“个人 API Key”
需要先设计 `User -> Tenant -> Key` 的归属与配额所有权，属于独立工作。

---

## 10. Technical debt

- **用户列表没有分页与检索**。`ListAdminUsers` 固定上限 500 条：校园规模够用，
  但开放注册正式投产前需要
  `GET /admin/users?page_size=50&cursor=...&q=zhangsan`。
- **注册成功但 Session 创建失败**时，账号已存在而客户端拿到 500，用户会以为注册失败
  并重试（然后撞用户名重复）。需要一个 `account_created_login_failed` 之类的响应，
  但不要在 Session 体系上做大型事务重构。
- 表名仍是 `gateway_admin_user` / `gateway_admin_session`，Go 类型仍是
  `model.AdminUser`。它们现在承载全部账号（含 member），名字属于历史遗留。
  **本轮刻意没有重命名：** 已是经过验证的 Session Auth v2 套件，为改名字做一次
  表迁移不符合“先做好功能”的目标。等账户体系稳定后，再单独做
  `gateway_admin_user -> gateway_user` 的机械迁移。
- `POST /auth/register` 是新增路径，而 `/admin/login` 等旧路径保持不变（它们已被
  Session Auth v2 的客户端与测试依赖）。将来统一成 `/auth/*` 需要单独做兼容层。
