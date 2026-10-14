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

## 6. 普通用户看到什么

member 登录后不会进入总览：它没有 `/admin/status` 权限，进入只会到处 403。
它看到的是「我的账号」页：

- 账号名、角色、状态
- 一段说明：当前没有网关管理权限，如需权限请联系超级管理员
- 修改密码、登录设备、撤销设备、退出登录（与管理员完全同一套组件）

member 唯一被允许的后端能力是自助端点：`/admin/whoami`、`/admin/password`、
`/admin/sessions*`、退出登录。其它所有管理接口一律 403。

---

## 7. 与业务 Tenant / API Key 的关系

**本轮没有把账号绑定到 API Key。** 现有模型仍是：

```text
Tenant ──> API Key ──> 业务数据面
```

member 第一版只是控制面账号，不拥有任何 API Key、配额或用量。要做“个人 API Key”
需要先设计 `User -> Tenant -> Key` 的归属与配额所有权，属于独立工作。

---

## 8. Technical debt

- 表名仍是 `gateway_admin_user` / `gateway_admin_session`，Go 类型仍是
  `model.AdminUser`。它们现在承载全部账号（含 member），名字属于历史遗留。
  **本轮刻意没有重命名：** 已是经过验证的 Session Auth v2 套件，为改名字做一次
  表迁移不符合“先做好功能”的目标。等账户体系稳定后，再单独做
  `gateway_admin_user -> gateway_user` 的机械迁移。
- `POST /auth/register` 是新增路径，而 `/admin/login` 等旧路径保持不变（它们已被
  Session Auth v2 的客户端与测试依赖）。将来统一成 `/auth/*` 需要单独做兼容层。
