# 统一用户账户体系与开放注册 —— 验收报告

日期：2026-10-13（验收）／2026-10-14（提交与 PR）
分支：`feature/user-account-registration`
基线：`main` @ `18410c95`（Admin UI v3 已合并）
HEAD：代码状态为 `6dbade0`（加固 6 个提交 `18f9e1f` → `6dbade0`）；PR #22 上的最终提交以该分支为准
PR：#22 `feat(auth): add public registration and user role management`
CI：代码状态 `6dbade0` 对应 run 38603700992 PASS；其后仅有文档提交，CI 亦为 success
    （Install / Check / Build Admin UI + go Test + go Build 全 success）
合并：**未合并**，等确认

> **后续修订（2026-10-15，Phase 1 安全加固）**：本报告记录的是其日期的验收状态，
> 其中以下行为已被 Phase 1 有意的修订取代，本文不作改写以免混淆历史记录：
>
> - `PUBLIC_REGISTRATION_ENABLED` 默认由 `true` 改为 `false`，需显式开启
>   （见 `docs/ACCOUNTS.md` 与 `docs/CODEX_HANDOFF_PHASE1.md` P1-03）。
> - `trusted-header` 未映射 / 未知角色不再兜底为 `auditor`，改为 `member`
>   （P1-01）。
> - 全局 `tenant_admin` 不再授予租户 / API Key 管理权限，持有者等同 `member`
>   （P1-02）。
> - 注册成功但自动登录失败返回 `503 account_created_login_failed`，账号保留
>   可直接登录（P1-07）。
> - Schema 迁移改由 `cmd/migrate` 统一管理（P1-08）。
>
> Phase 1 的最终交付以 `docs/PHASE1_COMPLETION_REPORT_2026-10-15.md` 为准。

---

## 1. 结论

```text
Admin UI check                 PASS
Admin UI build                 PASS
Go test / vet / build          PASS     195 个用例（基线 157）
dev.ps1 -ForceRestart -NoBrowser   READY / EXITCODE 0

Registration API                 PASS
First super_admin setup          PASS
Public member registration       PASS
Auto login after register        PASS
Duplicate username protection    PASS
Registration rate limit          PASS
Privilege escalation protection  PASS

Member login                     PASS
Member AccountPage               PASS
Member admin API denial          PASS

User management                  PASS
Role promotion                   PASS
Role demotion                    PASS
Session revoke after role change PASS
Disable / enable account         PASS
Last super_admin protection      PASS

Password change                  PASS
Session list                     PASS
Refresh rotation                 PASS
Concurrent refresh               PASS
Trusted-header regression        PASS
Token-mode regression            PASS
RBAC regression                  PASS

Existing dev user data deleted   NO
Database migration added         NO
Quota changed                    NO
API Key logic changed            NO
ThinkWatch routing changed       NO
/v1 data plane changed           NO
```

---

## 2. 账户模型

```text
User
├── member           公开注册创建，无管理权限
├── auditor          只读观测：请求日志、管理审计、安全事件
├── operator         总览 + 模型连通测试
├── tenant_admin     租户与 API Key
├── gateway_admin    供应商、模型与线路、ThinkWatch 同步
└── super_admin      全部权限，含用户与权限管理
```

只有一种账号。管理员不是另一种账号，而是普通账号拥有管理角色。

`member` 与管理员角色互斥：`NormalizeAccountRoles` 在每条写入角色的路径上执行同一规则
（去掉未知角色、去重、出现任一管理角色即移除 `member`、空结果变成 `["member"]`），
所以 `["member","gateway_admin"]` 不可能被存储，也不会在后续被按两种解释读出。

### 首个 super_admin 的判定条件

`needsSetup` 与 `CreateFirstAdminUser` 的守卫都是「**是否存在 super_admin**」而不是「账号表是否为空」。
在此之前公开注册一律拒绝（`409 setup_required`），所以远程用户无法抢先用一个 member
把初始化路径关掉、让部署停在无人可管的状态。

```text
| 系统状态                        | /admin/setup            | POST /auth/register |
| 没有任何 super_admin            | 允许（受本机限制）      | 409 setup_required  |
| 已有 super_admin               | 403 setup_not_allowed   | 201 → member        |
| PUBLIC_REGISTRATION_ENABLED=false | 正常                   | 403 registration_disabled |
| token / trusted-header 模式    | 503 admin_api_disabled  | 404（端点不存在）    |
```

---

## 3. 后端改动

### 新增

| 文件 | 作用 |
|---|---|
| `internal/app/account_roles.go` | 角色常量、`NormalizeAccountRoles`、互斥规则 |
| `internal/app/account_registration.go` | `POST /auth/register`、注册限流接线、审计与安全事件 |
| `internal/app/account_users.go` | `GET /admin/users`、`PATCH /admin/users/{id}` |
| `internal/app/account_registration_test.go` | 37 个测试函数 |
| `internal/ratelimit/registration.go` | 按 IP 的注册限流器 |
| `internal/ratelimit/counter.go` | 抽出的 `INCR + TTL` Lua 脚本 |

### 注册接口

```http
POST /auth/register
```

- 请求体使用 `DisallowUnknownFields` 严格解码：携带 `roles` / `status` / `is_admin` /
  `auth_version` 等字段的请求被拒绝为 `400 invalid_request`，**而不是被悄悄忽略** ——
  这样提权尝试会出现在审计里而不是看不见。
- 角色由后端固定为 `["member"]`，不是接口参数。
- 成功后**自动登录**：创建 Session、返回 Access Token、写 HttpOnly Refresh Cookie，
  `login_method` 记为 `register`。
- 写入 `account_registered` 安全事件，含 user_id、username、来源 IP；不含密码或任何 Token。

### 注册限流

```text
PUBLIC_REGISTER_MAX_ATTEMPTS=5
PUBLIC_REGISTER_WINDOW=1h
```

- 按来源 IP 计数，**每次尝试都计一次**（包括用户名重复的失败）。如果只统计成功，
  脚本可以用一个已占用的用户名全速探测哪些名字存在。
- Redis 不可用时**拒绝**注册（`503 registration_unavailable`）。这与登录限流的 fail-open
  相反：计数器拿不到时无限注册比暂时不能注册更糟。

### 用户管理

```http
GET   /admin/users
PATCH /admin/users/{id}     body: {display_name?, roles?, status?}
```

- 只有 super_admin 能访问。网关 RBAC 允许列表里没有任何其它角色能碰到 `/admin/users`
  前缀——gateway_admin 若能用它就能把自己提成 super_admin。
- 响应中没有 `password_hash` / `refresh_hash` / `auth_version`。
- 改角色、停用/启用都提升 `auth_version` 并撤销该账号的所有 Session；改显示名称不提升版本、
  不踢人下线。

### 最后一个 super_admin 保护

在**写入事务内**用 advisory lock 校验，而不是在 service 层先查后写：

```text
pg_advisory_xact_lock(adminUserGuardLockKey)
  → 读取目标账号 FOR UPDATE
  → 若「目标是启用的 super_admin」且「变更后不再是」
      统计其它启用的 super_admin，为 0 则拒绝
  → apply
```

两个并发的降级请求会各自看到对方的 super_admin 然后都执行成功，最终系统无人可管 ——
这正是要用事务内锁的原因。违反时返回 `409 last_super_admin`。

### 模式兼容

token / trusted-header 模式下没有账户服务，`/auth/register` 返回 404（端点不存在），
`/admin/users` 返回 503 `admin_api_disabled`。

---

## 4. 为什么没有新增 Migration

规格要求「尽量不新增 Migration；如果确实需要，必须说明理由」。本 PR 一条也没有加，
现有 `gateway_admin_user` 已经具备所需的一切：

```text
username · password_hash · display_name · roles · status
auth_version · last_login_at · created_at · updated_at
```

用户与权限页顶部那四个统计数字（总用户 / 普通用户 / 管理员 / 停用账号）**没有**为每个数字
发一次请求，而是由 `AdminUserStats` 用一条聚合语句算出，所以它们不会互相矛盾，
也不会和并发注册竞争同一行数据：

```sql
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE NOT EXISTS (
         SELECT 1 FROM jsonb_array_elements_text(roles) AS r
         WHERE r IN ('auditor','operator','tenant_admin','gateway_admin','super_admin'))),
       COUNT(*) FILTER (WHERE EXISTS (
         SELECT 1 FROM jsonb_array_elements_text(roles) AS r
         WHERE r IN ('auditor','operator','tenant_admin','gateway_admin','super_admin'))),
       COUNT(*) FILTER (WHERE status <> 1)
FROM gateway_admin_user
```

`roles @> '["super_admin"]'::jsonb` 承担 super_admin 的存在性判断，因此 `needsSetup`
和首个管理员守卫都不需要新列、新索引或新视图。

表重命名（`gateway_admin_user` → `gateway_user`、`gateway_admin_session` → `gateway_user_session`）
按规格要求留到账户体系稳定后单独做，理由记录在 `docs/ACCOUNTS.md` 第 10 节。

---

## 5. 前端改动

### 统一登录入口

登录页改为一个卡片内的两个 tab（登录 / 注册）。注册是 `AuthPhase` 之外的能力，
因为 member 和管理员只是角色不同，不是两种入口。

- 注册表单：用户名、显示名称（可选）、密码、确认密码，含密码显隐、加载态、错误提示、
  重复用户名提示（中文）、密码规则提示。
- 提交成功即自动登录，不弹「注册成功请重新登录」。
- 认证错误码做了中文化映射（`lib/errors.ts`），账号被停用单独提示「当前账号已停用，
  请联系管理员。」

### 导航按角色过滤

`NavItem` 带上 `roles` 字段，与后端允许列表一致：

```text
member        → 我的账号
super_admin   → 全部页面 + 用户与权限
gateway_admin → 总览 / 供应商 / 模型与路由 / 系统设置
tenant_admin  → 总览 / 租户 / API Key / 系统设置
auditor       → 总览 / 请求日志 / 管理审计 / 安全事件 / 系统设置
operator      → 总览 / 系统设置
```

空组整体隐藏，member 不会看到一个空的「模型服务」标题。Dashboard 的快捷操作同样按角色过滤：
tenant_admin 与 auditor 看不到「同步 ThinkWatch」和「模型连通测试」，因为它们的后端允许列表里
没有这两个端点。

### 浏览器实测抓到的缺陷

浏览器实测抓到三个纯自动化测试没暴露的缺陷，都已修复并有回归测试：

1. **`useEffect` 放在提前 return 之后**（`App.tsx`）导致 hook 数量随渲染变化，
   React 直接白屏（`Rendered more hooks than during the previous render`）。
   已上移到所有提前 return 之前，并按 hooks 规则加了注释。
2. **token / trusted-header 模式下 `s.adminAuth` 为 nil**，用户管理接口空指针 panic。
   两个 handler 都补上了模式检查，并加了回归测试
   （`TestUserManagementRefusedOutsideSessionMode`，逐一覆盖两种模式）。

另有一个 UX 缺陷：Toast provider 挂在根上，退出再登录后上一个人的消息还在屏幕上
——包括一条关于**别人**权限的通知。已在账号切换处清空。

---

## 6. 浏览器实测（A–G 全链路）

用无头 Edge + CDP 驱动真实界面，跑完整链路。全套 7 张截图见
`docs/screenshots/accounts/`。

```text
A  setup                 setup 界面出现，registration tab 不出现
                         setup 成功 → 导航含全部 9 项 + 用户与权限
B  退出                  登录页出现 登录 / 注册 两个 tab
C  注册                  字段：用户名 / 显示名称（可选）/ 密码 / 确认密码
                         用户名重复 → "该用户名已被使用，请换一个。"
                         注册成功 → 自动登录
                         落地页 = 我的账号，身份显示 普通用户
                         导航 = ["我的账号"]，管理页面 0 个
D  退出
E  提权                 用户与权限进入正常
                         统计 = 总用户 2 / 普通用户 1 / 管理员 1 / 停用 0
                         表格行 = test-member | 测试成员 | 普通用户 | 正常 | 刚刚 | ... | 管理
                         campus-admin | — | 超级管理员 | 正常 | 刚刚 | ... | 管理
                         保存 → "权限已更新，该用户现有登录会话已失效。"
F  提权后登录           导航 = 总览 / 供应商 / 模型与路由 / 系统设置
                         无 用户与权限 / 租户 / API Key
                         可进入 供应商 页
G  降权后登录           导航回到 ["我的账号"]
console error / 运行时错误    0
```

数据面与配置一致性：

```text
GET /v1(models)        200
campus-smart BEFORE    200
POST thinkwatch/sync   200  {"providers":2,"aliases":1,"ok":true}
campus-smart AFTER     200
SSE AFTER              200
request log provider   qwen（真实落库）
ThinkWatch 配置         与运行前备份逐字节一致
```

模式回归（独立实例实测）：

```text
token 模式        /auth/register → 404      /admin/users → 503    panic 0
trusted-header    /auth/register → 404
                 无请求头 → 401；伪造用户头无密钥 → 401
                 super_admin    users=503   （无账户服务，符合预期）
                 gateway_admin  users=403
                 auditor        users=403
                 operator       users=403
                 tenant_admin   users=403
                 member         users=403 status=403 providers=403
                 无角色时的 fallback 仍是 auditor（本地 member 不受影响）
```

用户数据库全程未动：2 租户 / 3 Key / 2 供应商 / 1 对外模型 / 1 管理员账号（campus-admin，
`["super_admin"]`）。

---

## 7. 截图

`docs/screenshots/accounts/`

```text
01-dashboard-super-admin.png    setup 后 super_admin 的总览
02-register-form.png            注册表单
03-member-account.png           member 的「我的账号」首页
04-user-management.png          用户与权限
05-role-drawer.png              角色管理抽屉
06-promoted-console.png         提权后 gateway_admin 控制台
07-after-demotion.png           降权后回到普通用户页
```

---

## 8. Pre-merge security hardening

代码审查在合并前发现 7 个问题，本轮全部修复。

### 8.1 注册限流：两阶段改成一次原子 Consume（P1）

原来的接口是 `AllowRegistration()` 再 `RecordRegistration()`，中间存在
time-of-check-to-time-of-use 窗口：限制为 5 时，同时到达的 50 个请求可以全部读到
计数 4、全部判定通过、再各自 INCR —— 一次突发就能放进远超策略数量的账号。
这是公开注册 anti-abuse 的直接绕过。

现在是一次调用完成 INCR + TTL + 判定：

```text
ConsumeRegistration(ctx, ip) (allowed bool, retryAfter time.Duration, err error)
```

Lua 脚本先自增再回答，因此第 5 个调用者拿到最后一个名额，第 6 个被拒绝，
与并发数无关。判定是 `current <= maxAttempts`，即上限 5 恰好放行 5 个（不是 4 个）。

**TTL 不会被攻击请求延长**：脚本只在 `current == 1` 时设置过期，窗口从第一个请求起算；
`ttl < 0` 的补设分支只用于修复异常丢失 TTL 的 key。反复重试无法把重置时间推远。

`PUBLIC_REGISTER_MAX_ATTEMPTS=0` 仍然表示关闭限流，此时**完全不访问 Redis**。

Redis 不可用时**仍然 fail closed**（`503 registration_unavailable`）；登录限流的
fail-open 策略保持不变，两者没有统一。

消耗时机也固定下来：在基础校验（username 格式、display_name 长度、密码策略）、
setup 与 registration-enabled 判定**之后**，`CreateUser` **之前**消费一次。
因此 malformed JSON / 弱密码 / 确认不一致 / 超长用户名 / 携带安全字段的请求不计费，
而**用户名重复必须计费** —— 否则可以用一个已知存在的名字免费探测接口。

### 8.2 统一用户 Mutation：一次事务、一次 UPDATE、auth_version 最多 +1（P1/P2）

原来 roles / status / display_name 走三个独立 storage mutation，由此产生四个问题：

- 一个 PATCH 同时改 roles 和 status 时 `auth_version` **加两次**
- 只改显示名称也会踢用户下线（前端总是发送三个字段）
- 无变化的 PATCH 仍然 bump 版本
- 多字段 PATCH 不是原子的：因 last-super-admin 被拒绝时，display_name 可能已经写进去了

现在合成一次调用：

```text
UpdateAdminUserAccount(ctx, id, mutation) (model.AdminUser, AdminUserChangeSet, error)
```

事务内顺序：`BEGIN` → `pg_advisory_xact_lock` → `SELECT ... FOR UPDATE` → 计算最终状态 →
**按最终状态做一次** last-super-admin 判定 → 一次 UPDATE → `COMMIT`。

**advisory lock 必须保留**，不能只靠行锁：两个降级请求可能改的是**不同的行**
（super1 与 super2），行锁各自生效、各自看到对方仍是 enabled，于是双双提交，
最终 0 个 super_admin。锁是跨行的全局串行化点。

一次 UPDATE 用 CASE 表达版本语义：

```sql
auth_version = CASE WHEN $5 THEN auth_version + 1 ELSE auth_version END,
updated_at   = CASE WHEN $6 THEN NOW()   ELSE updated_at   END
```

`auth_version` 语义固定为：

| 变更 | auth_version | 会话 |
|---|---|---|
| 仅 display_name | +0 | 保留 |
| 仅 roles | +1 | 撤销 |
| 仅 status | +1 | 撤销 |
| roles + status | +1 | 撤销一次 |
| roles + display_name | +1 | 撤销一次 |
| status + display_name | +1 | 撤销一次 |
| 三者同时 | +1 | 撤销一次 |
| no-op | +0 | 保留，且 `updated_at` 不变 |

**no-op 根本不执行 UPDATE**。`AdminUserChangeSet` 由前后状态比较得出，
角色比较走 canonical 形式，所以「同样的角色换了个顺序」不算变化。

`RevokeSessionsForUser` 每个逻辑 mutation **只调用一次**，原因按实际变化记录
（`role_changed` / `account_disabled` / `account_status_changed` / `account_security_changed`）。
它仍然是 best effort：事务已经 bump 了 auth_version，旧 Token 在下一次请求就会失效，
清理 session 行失败不能回滚已经成功的权限变更。

未知角色不再静默丢弃：`ValidateAccountRoles` 让 `["gateway_admin","god_mode"]`
返回 `400 invalid_request`。`NormalizeAccountRoles` 仍负责去重、互斥与排序。

### 8.3 审计按真实 ChangeSet（P1/P2）

原来按「请求里有没有 roles 字段」判断，会把「字段存在但值没变」记成一次角色变更。
现在按 `AdminUserChangeSet` 记录，且**多字段 PATCH 写多条语义事件**：

```text
roles + disable  →  user.role.update + user.disable
roles + profile  →  user.role.update + user.profile.update
三字段           →  user.role.update + user.disable/enable + user.profile.update
no-op            →  不写任何业务审计
```

Actor 是执行操作的 super_admin，Resource 是 `/admin/users/<targetID>`。

### 8.4 operator 模型连通测试（P2）

operator 的允许列表里有 `GET /admin/status` 与 `POST /admin/model-test`，Dashboard 也
展示「模型连通测试」按钮，但打开抽屉需要 `GET /admin/models`，而 operator 没有该权限
—— 按钮点了必然 403。

不能靠把 `/admin/models` 开给 operator 解决：那是完整的模型管理接口。
新增 `GET /admin/model-test-options`，只返回 UI 真正需要的三个字段：

```json
{"data":[{"id":"...","alias":"campus-smart","status":1}]}
```

`modelTestOptionView` 是独立视图类型，不是 `json.Marshal(ModelConfig)` ——
这样 `ModelConfig` 将来新增字段也不会自动泄漏到受限接口。前端新增
`ModelTestOption` 类型（不再伪装成 `ModelConfig`），Dashboard 改用它取模型列表。

RBAC：super_admin / gateway_admin / operator 允许；tenant_admin / auditor / member 403。
operator 对 `/admin/models` 与 `/admin/providers` 仍然 403。

### 8.5 前端只发送真正变化的字段（P2）

`UserDrawer` 原来总是发送 `{roles, status, display_name}`，因此只改显示名称也会被
当成角色变更并踢用户下线。现在构造 partial patch，只包含真正变化的字段；
无变化时保存按钮 disabled 且不发 HTTP。

角色比较用 canonical 形式（`canonicalRoles`，排序后 join），避免顺序变化被误判。

Toast 按后端返回的真实 ChangeSet 区分：

```text
仅 display_name → 用户信息已更新。
仅 roles        → 权限已更新，该用户现有登录会话已失效。
停用            → 账号已停用，该用户现有登录会话已失效。
启用            → 账号已启用，用户可以重新登录。
roles + status  → 用户设置已更新；权限或状态变更已使该用户现有登录会话失效。
```

### 8.6 本轮验证

```text
go test -count=1 ./...                      228 个用例（基线 195）
go test -race                               SKIPPED —— 本机无 C 编译器（gcc 不在 PATH），
                                            -race 需要 cgo，如实记录未伪造
真实 Redis 原子限流测试                      50 goroutine / 上限 5 → 恰好 5 通过 45 拒绝
真实 PostgreSQL 并发 super_admin 测试        两行并发降级 → 1 成功 1 ErrLastSuperAdmin，
                                            最终 enabled super_admin == 1
真实 PostgreSQL 跨字段并发测试               roles 与 status 改不同行 → 同样 1 成功 1 拒绝
API 级验收                                   52 / 52
浏览器 UI 验收                               ALL PASS（0 console error）
/v1 数据面 smoke                             models / non-stream / SSE 全 200，provider=qwen
```

---

## 9. 提交

```text
feat(account): add the member role and a single normalised role vocabulary
feat(account): add public registration with rate limiting and auto sign-in
feat(account): add super-admin user and role management
test(account): cover registration, escalation and mode boundaries
feat(admin-ui): add registration, the member home page and user management
docs(account): document the unified account model and open registration
```

---

## 10. Known remaining items

本轮明确不做，但需要在开放注册正式投产前解决：

```text
1. 用户列表分页与检索
   ListAdminUsers 目前固定上限 500 条，够校园规模但不够生产。
   建议：GET /admin/users?page_size=50&cursor=...&q=zhangsan
   本轮刻意不扩大 PR #22 的范围。

2. CreateUser 成功后 openSession 失败
   账号已创建但客户端拿到 500，用户会以为注册失败并重试（然后撞用户名重复）。
   本轮不做大型事务重构；如果以后要小成本改善，可以返回
   account_created_login_failed 并提示「账号已创建，请直接登录」。

3. CAPTCHA / Turnstile
4. 邮箱验证 / 邀请码 / 管理员审批
5. 忘记密码、密码自助重置
6. 校园 SSO 自动开户（trusted-header identity 暂不写入用户表）
7. 个人 API Key、用户计费、个人模型调用、用户充值与额度
   （需要先设计 User -> Tenant -> Key 的归属与配额所有权）
8. gateway_admin_user → gateway_user 表重命名
   （技术债，见 docs/ACCOUNTS.md 第 10 节）
```

---
