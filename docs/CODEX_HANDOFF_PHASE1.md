# Campus AI Gateway Phase 1 开发交接

> 面向执行 Agent：WorkBuddy  
> 交接与独立复核：Codex  
> 基线日期：2026-10-15  
> 仓库：`Jiusheng01/campus-ai-gateway`  
> 分支：`feature/user-account-registration`  
> 基线提交：`ca9ec4a`  
> PR：#22 统一账户体系与开放注册

## 1. 文档目的

本文档把已经完成并经用户确认的 Grilling 结果转换为 WorkBuddy 可直接执行的 Phase 1 实施计划。

本轮只处理 PR #22 的安全问题和合并阻断项，不实现长期架构中的校园 SSO 自动开户、用户与租户关联、个人 API Key、三层配额、租户成员管理、TOTP、多因素认证或用户门户。

本文档本身是当前 Codex 轮次唯一允许产生的仓库变更。Codex 不修改业务代码，不提交、不推送、不合并 PR。WorkBuddy 后续实施时也不得自行部署到真实环境或修改用户数据。

## 2. 当前仓库事实

### 2.1 Git 状态

- 当前分支：`feature/user-account-registration`
- 当前 HEAD：`ca9ec4a`
- 相对 `origin/main`：领先 14 个提交，落后 0 个提交
- PR #22 涉及 55 个文件，主要增加公开注册、`member`、用户管理、角色管理和相关测试
- 当前工作区已有未跟踪目录 `.workbuddy-ai/`，属于用户现有内容，任何实施不得修改、删除、移动或纳入提交
- 本交接生成前，`docs/CODEX_HANDOFF_PHASE1.md` 不存在

### 2.2 已有实现

- Go 1.24、`net/http`、PostgreSQL/pgx、Redis
- React 18、TypeScript、Vite，前端构建后由 Go embed 提供 `/admin-ui`
- 三种现有认证模式：
  - `session`：本地账号密码、短期 Access Token、HttpOnly Refresh Cookie
  - `token`：单一 `ADMIN_TOKEN` 应急模式
  - `trusted-header`：认证代理注入身份与角色
- Access Token 只保存在前端内存；Refresh Token Cookie 使用 `HttpOnly`、`SameSite=Strict`
- Refresh Token 只在数据库保存摘要，已有轮换、并发宽限和重放检测
- 已有最后一个启用 `super_admin` 的事务级保护
- PR #22 增加公开注册、`member`、六角色模型、用户列表与角色修改
- 当前 `go test ./...` 通过，但测试通过不表示本交接列出的安全边界已经满足

### 2.3 已确认问题

1. `member` 账号没有数据面能力，只能管理自己的密码和会话。
2. 当前全局 `tenant_admin` 可访问所有租户和全部 API Key，没有资源范围。
3. `trusted-header` 缺少有效角色时默认授予 `auditor`，会暴露全局请求日志、管理审计和安全事件。
4. 公开密码注册默认开启，与已经确认的生产 SSO 策略冲突。
5. `logout` 只从 Refresh Token 解析 `sid`，未验证 Token 摘要，知道 `sid` 即可撤销对应会话。
6. 认证 JSON 解码未拒绝第二个 JSON 值。
7. Admin UI 响应缺少必要的浏览器安全头。
8. 用户创建成功但 Session 创建失败时，账号已落库，客户端却只收到通用失败，随后重试会遇到用户名冲突。
9. `deploy/docker-compose.yml` 只挂载 `0001_init.sql`，全新部署不能可靠应用 `0002` 到 `0009`。
10. 用户列表固定最多 500 条、缺少分页与检索；此问题在注册默认关闭后不阻断 Phase 1 合并，但必须在开放真实用户入口前解决。

## 3. 完整长期设计基线

本节记录全部已确认决策。它们是后续阶段的约束，不等于 Phase 1 的实施范围。

### 3.1 身份与认证

- 正式环境以校园 SSO 为主入口。
- SSO 优先通过 `oauth2-proxy` 等认证代理接入；网关只信任受保护代理注入的身份头。
- 后端不得被终端用户绕过代理直接访问；代理必须删除客户端伪造的身份头。
- SSO 身份使用 `issuer + subject` 作为稳定唯一标识，姓名、用户名、邮箱和学号均为可更新属性。
- SSO 首次登录自动创建 `member` 账号和个人租户，但该能力属于后续阶段。
- SSO 提供身份和组织属性；网关数据库拥有最终的全局角色和租户内角色。
- 缺少有效角色时只能得到 `member`，不得回退为 `auditor`。
- 本地账号只作为应急管理入口，限制在管理内网，强制 TOTP 和离线恢复码。
- 至少保留两个由不同负责人管理的应急管理员；重置多因素认证需要双人审批。
- SSO 故障时，已有 SSO 会话可在原有效期内继续；普通用户不得自动降级为本地密码登录。

### 3.2 用户、租户与成员关系

- 用户与租户为多对多关系。
- 每位有效校园用户拥有个人租户，也可以加入多个院系、课程、课题组等组织租户。
- 组织租户需要全局运营人员审批创建；后续可与学校组织系统同步。
- 租户成员来源分为 `idp` 和 `local`：
  - `idp` 来源由身份系统同步，本地租户管理员不能覆盖；
  - `local` 来源由租户管理员邀请和撤销。
- 人工邀请绑定校园身份或学校验证邮箱，默认 7 天有效，用户通过 SSO 登录并确认后生效。
- 租户必须始终保留至少一个有效 `tenant_admin`；并发变更必须使用事务级保护。
- 跨租户请求统一返回 `404 Not Found`，同时记录安全事件，避免泄露资源是否存在。
- 后端查询必须携带授权范围中的 `tenant_id`，不得先读取任意资源后再依赖前端隐藏。

### 3.3 角色隔离

全局控制面角色采用可组合的最小权限模型：

- `auditor`：查看全局日志和安全事件
- `operator`：执行模型连通测试
- `gateway_admin`：管理供应商、模型和路由
- `tenant_operator`：审批组织租户并配置租户额度上限
- `super_admin`：管理用户、全局角色和应急事项

租户内角色独立存放，不得写入全局账号角色字段：

- `tenant_admin`：管理本租户成员、API Key 和租户设置
- `developer`：管理自己的 Key，并查看授权范围内的用量
- `viewer`：只读查看允许公开的租户用量

当前全局 `tenant_admin` 命名和授权模型不符合该基线，Phase 1 必须先停止其全局资源访问；真正的租户内角色在后续成员关系模型中实现。

### 3.4 API Key 与服务账号

- 用户在门户中手动创建 API Key，首次登录不得自动生成 Key。
- API Key 归创建者管理，租户管理员可以管理本租户全部 Key。
- Key 保存 `tenant_id` 和 `created_by_user_id`，明文只展示一次，数据库只保存不可逆摘要。
- 个人租户每人每租户最多 3 个有效 Key，默认 90 天。
- 组织开发 Key 默认 180 天。
- 服务账号 Key 需要租户管理员审批，最长 365 天，并指定至少两名负责人。
- Key 轮换允许短暂双 Key 并存；到期前发送通知。
- 模型权限由租户范围向 Key 收窄，Key 不能扩大租户已有权限。
- 高成本或敏感模型需要额外审批。

### 3.5 三层配额

- 租户层：月度预算、Token 和请求硬上限。
- 成员层：从租户额度中分配个人额度。
- API Key 层：RPM、TPM、最大并发和模型范围。
- 下层限制不能绕过上层硬限制。
- 成员额度由本租户 `tenant_admin` 审批。
- 租户总额度和高成本模型由全局 `tenant_operator` 审批。
- 审批记录必须包含旧值、新值、理由、申请人、审批人和时间。

### 3.6 生命周期、日志与隐私

- 账号状态：`active`、`suspended`、`deprovisioned`；临时登录锁定单独建模。
- IdP 确认身份失效后，停用账号、撤销会话、停用个人租户 Key。
- 共享组织租户 Key 不因创建者离校自动失效，但必须保留 `created_by` 并可由租户管理员接管。
- 不硬删除历史审计；超过保留期后按学校政策清除或脱敏姓名、邮箱和学号。
- 默认不保存提示词和模型回复。
- 故障排查如需内容，必须显式授权、脱敏、最长保留 7 天并自动删除。
- 默认保留策略：逐条请求元数据 90 天，管理审计和安全事件至少 365 天，用量汇总 3 年，过期会话 30 天。
- 新设备登录、Key 和角色变更、配额审批通知本人；重放、应急管理员登录和连续越权通知安全管理员。

### 3.7 产品界面与接口边界

- 同一前端工程内明确区分用户门户和管理控制台。
- 后续接口边界为 `/api/me/*`、`/api/tenants/{id}/*` 和 `/admin/*`。
- 用户门户包括个人 Key、用量、租户成员关系、配额申请和账号安全。
- 管理控制台包括供应商、模型、全局租户、审计和角色管理。
- 旧认证接口保留一个发布周期并提供弃用信息，随后统一到 `/auth/*`。

## 4. Phase 1 目标与非目标

### 4.1 目标

Phase 1 的目标是让 PR #22 在不引入后续业务模型的前提下满足安全合并条件：

1. 未映射的 SSO 身份不再获得审计权限。
2. 未作用域化的全局 `tenant_admin` 不再访问全部租户和 Key。
3. 公开注册改为明确选择后才开启，生产默认关闭。
4. 登出请求必须持有与会话匹配的 Refresh Token。
5. 认证 JSON 严格限制为一个合法值并保留未知字段拒绝规则。
6. Admin UI 返回基础浏览器安全头。
7. 注册成功但自动登录失败时返回可恢复、无歧义的结果。
8. 部署必须通过独立迁移步骤应用所有迁移，失败时网关不得启动。
9. 保持现有 Session Auth v2、安全不变量、数据面和公开 API 兼容。

### 4.2 明确非目标

Phase 1 禁止实现：

- SSO JIT 自动开户或 `issuer + subject` 数据模型
- `session + trusted-header` 混合认证模式
- TOTP、WebAuthn、恢复码或双人审批
- 用户与租户成员关系表
- 个人租户、组织租户申请或邀请流程
- `tenant_operator` 新角色及完整工作流
- 个人 API Key、服务账号或 Key 所有者字段
- 成员级配额、三层配额或高成本模型审批
- 用户门户和 `/api/me/*`、`/api/tenants/*`
- 账号 `active/suspended/deprovisioned` 新状态模型
- 邮件通知、离校回收、数据匿名化或清理任务
- 用户列表分页与检索
- 表名 `gateway_admin_user`、`gateway_admin_session` 重命名
- 认证接口全面迁移到 `/auth/*`
- 与真实学校 IdP、真实邮件、真实 Provider 或生产网关的集成

## 5. Phase 1 执行顺序

建议严格按以下顺序实施。每个任务单独形成可审查的提交候选，但 WorkBuddy 不得自行提交或推送，除非用户另行授权。

```text
P1-00 基线保护与测试清单
  ├─ P1-01 trusted-header 最小权限回退
  ├─ P1-02 停止未作用域化 tenant_admin 的全局授权
  ├─ P1-03 公开注册改为显式启用
  ├─ P1-04 logout 校验 Refresh Token 所有权
  ├─ P1-05 严格 JSON 请求边界
  ├─ P1-06 Admin UI 浏览器安全头
  ├─ P1-07 注册成功但自动登录失败的可恢复语义
  └─ P1-08 独立数据库迁移命令与部署门禁
        ↓
P1-09 全量回归、文档同步和交付报告
```

P1-01 到 P1-08 可在代码层独立开发，但 P1-09 必须在全部任务完成后执行。

## 6. 任务明细

### P1-00 基线保护与测试清单

#### 根因

PR #22 已经过多轮加固，涉及认证、会话、RBAC、注册限流和真实存储集成测试。若不先固定基线，安全修复可能破坏已验证的不变量。

#### 关联文件

- `docs/ACCOUNTS.md`
- `docs/SSO_RBAC.md`
- `docs/ACCOUNT_REGISTRATION_REPORT_2026-10-13.md`
- `internal/app/admin_session_test.go`
- `internal/app/admin_auth_http_test.go`
- `internal/app/account_registration_test.go`
- `internal/app/account_mutation_test.go`
- `internal/storage/admin_users_integration_test.go`
- `internal/ratelimit/registration_test.go`
- `scripts/e2e-admin-auth-v2.sh`

#### 修改边界

- 不修改业务逻辑。
- 不重写既有测试框架。
- 记录基线测试结果、跳过原因和环境限制。
- 不操作用户现有 PostgreSQL、Redis、ThinkWatch 或 Docker volume。

#### 实施步骤

1. 确认分支和 HEAD 与本文档一致；若不一致，停止并向用户报告。
2. 确认工作区除本文档和 WorkBuddy 后续明确修改外，没有未知变更。
3. 运行无状态测试，保存完整输出。
4. 建立 Phase 1 测试矩阵，将每个安全修复与至少一个失败前测试和修复后断言对应。

#### 验收标准

- 基线状态和测试结果写入最终交付报告。
- 所有跳过项都说明原因，禁止把 `SKIPPED` 写成 `PASS`。
- 不触碰 `.workbuddy-ai/` 和用户已有数据。

### P1-01 trusted-header 最小权限回退

#### 根因

`internal/app/admin_auth.go` 在代理未提供角色或只提供未知角色时添加 `auditor`。`auditor` 能读取全局请求日志、管理审计和安全事件，违反默认拒绝和最小权限原则。

#### 关联文件

- `internal/app/admin_auth.go`
- `internal/app/admin_auth_test.go`
- `internal/app/account_registration_test.go`
- `docs/SSO_RBAC.md`
- `docs/ACCOUNTS.md`
- `scripts/e2e-admin-auth-v2.sh`

#### 修改范围

- 空角色、全空白角色、只包含未知角色时，规范化结果必须为 `member`。
- 明确且已知的全局角色继续按当前兼容行为解析。
- 未提供 `ADMIN_PROXY_SECRET`、代理密钥错误或用户头为空仍返回未认证，不得因此变成 `member`。
- 后端权限是唯一安全边界；前端隐藏仅用于体验。
- 不实现 SSO JIT、数据库角色映射或混合认证。

#### 正常流程

1. 可信代理提供正确代理密钥和用户身份。
2. 角色头包含明确允许的全局角色。
3. 网关生成对应 principal，并按现有允许列表授权。

#### 失败路径与安全边界

- 缺少代理密钥、密钥错误、身份为空：`401`。
- 身份有效但角色为空：仅 `member`，可以读取 `whoami`，所有管理资源返回 `403`。
- 角色全部未知：仅 `member`，不保留未知角色。
- `member,auditor` 等组合继续经过统一规范化，不允许不同代码路径产生不同解释。

#### 回归用例

- 空角色头不能访问 `/admin/status`、`/admin/request-logs`、`/admin/admin-audit`、`/admin/security-events`。
- 未知角色不能访问任何管理资源。
- 明确 `auditor` 仍可读取既有只读资源，不能修改资源。
- 明确 `gateway_admin`、`operator`、`super_admin` 保持现有授权。
- 伪造用户头但无代理密钥仍为 `401`。

#### 验收标准

- 不存在任何默认授予 `auditor` 的分支。
- 新增测试在旧实现上失败，在修复后通过。
- 文档、单元测试和 E2E 的角色回退描述一致。

### P1-02 停止未作用域化 tenant_admin 的全局授权

#### 根因

当前 `adminPrincipal.can` 只根据全局角色字符串和路径前缀判断。持有 `tenant_admin` 的主体可以管理全部租户和全部 API Key，没有 `tenant_id` 范围。这与长期设计中的租户内 `tenant_admin` 含义冲突。

#### 关联文件

- `internal/app/account_roles.go`
- `internal/app/admin_auth.go`
- `internal/app/admin_session.go`
- `internal/app/account_users.go`
- `internal/app/account_registration_test.go`
- `internal/app/admin_session_test.go`
- `web/admin/src/lib/roles.ts`
- `web/admin/src/app/navigation.ts`
- `web/admin/src/pages/UsersPage.tsx`
- `docs/ACCOUNTS.md`
- `docs/SSO_RBAC.md`

#### 修改范围

- Phase 1 采取 fail closed：移除全局 `tenant_admin` 对 `/admin/tenants*` 和 `/admin/api-keys*` 的授权。
- 禁止在全局角色管理界面继续授予 `tenant_admin`。
- 通过 API 新授予全局 `tenant_admin` 必须返回 `400 invalid_request`，不得静默改成其他角色。
- 数据库中已存在的全局 `tenant_admin` 不做自动数据迁移、不自动提升为 `super_admin` 或未来的 `tenant_operator`。
- 读取到遗留 `tenant_admin` 时必须无管理权限；允许在用户管理界面由 `super_admin` 显式改成其他现有角色。
- 不新增租户成员表，不实现真正的租户内 `tenant_admin`。

#### 正常流程

- `super_admin` 继续管理全部资源。
- 其他现有全局角色只访问其既有范围。
- 用户管理页面不再把 `tenant_admin` 作为可分配的全局角色。

#### 失败路径与安全边界

- Session 或 SSO principal 仅含遗留 `tenant_admin`：租户与 Key 接口返回 `403`。
- `PATCH /admin/users/{id}` 尝试授予 `tenant_admin`：`400 invalid_request`，目标账号和会话不变。
- trusted-header 只传 `tenant_admin`：按无有效全局角色处理为 `member`。
- 不得用前端隐藏代替后端拒绝。

#### 回归用例

- 遗留 `tenant_admin` 对租户列表、租户修改、Key 列表、Key 创建和撤销全部为 `403`。
- `super_admin` 原有租户和 Key 管理能力不变。
- `gateway_admin` 仍不能访问租户和 Key。
- `auditor`、`operator`、`member` 权限不扩大。
- `member` 与有效管理角色互斥、未知角色拒绝、角色排序等原有测试继续通过。

#### 验收标准

- 后端不存在全局 `tenant_admin` 的资源授权分支。
- 前端不展示该全局角色的可分配选项或导航入口。
- 处理遗留值时只允许降权，不允许隐式改成更高权限。

### P1-03 公开注册改为显式启用

#### 根因

`PUBLIC_REGISTRATION_ENABLED` 当前默认 `true`，而已确认的正式架构以校园 SSO 为入口。现阶段 `member` 没有数据面能力，默认开放匿名密码注册既无业务价值，也增加滥用面。

#### 关联文件

- `internal/app/config.go`
- `internal/app/server.go`
- `internal/app/admin_auth_handlers.go`
- `internal/app/account_registration.go`
- `internal/app/account_registration_test.go`
- `.env.example`
- `README.md`
- `docs/ACCOUNTS.md`
- `web/admin/src/auth/LoginPage.tsx`

#### 修改范围

- 未设置环境变量时默认 `PUBLIC_REGISTRATION_ENABLED=false`。
- `.env.example` 使用 `false`，并明确公开注册仅供受控测试或未来明确启用。
- 显式设为 `true` 时保留当前注册接口、限流、`member` 固定角色和自动登录行为。
- 注册关闭时，Session 模式 `POST /auth/register` 继续返回当前已定义的 `403 registration_disabled`。
- token 和 trusted-header 模式继续返回 `404`，不得改变端点存在性契约。
- 注册关闭时前端不得显示注册页签。
- 不删除注册代码，不实现 SSO 注册替代方案。

#### 正常流程

- 默认配置：Setup 仍可创建首个本地 `super_admin`，登录可用，注册入口隐藏。
- 显式开启：完成 Setup 后显示注册入口，创建固定 `member`，保持现有限流和审计。

#### 失败路径与安全边界

- Setup 未完成时，即使显式开启注册，也继续返回 `409 setup_required`。
- 注册关闭时直接请求接口返回 `403`，不创建用户、不创建会话、不消耗注册限流额度。
- Redis 不可用且注册显式开启时，注册继续 fail closed。
- 请求携带 `roles`、`status`、`is_admin`、`auth_version` 等未知或安全字段继续返回 `400`。

#### 回归用例

- 默认配置的 `auth-config.registration_enabled=false`。
- 显式 `true` 且 Setup 完成后为 `true`。
- 登录页与配置一致，不出现幽灵注册入口。
- Setup、登录、Refresh、Logout 不受默认值变化影响。
- 原子注册限流的并发、TTL 和 fail-closed 测试全部保留。

#### 验收标准

- 新部署在未配置开关时不开放密码注册。
- 显式启用仍完全兼容当前注册 API。
- 文档不再暗示生产环境默认公开注册。

### P1-04 logout 校验 Refresh Token 所有权

#### 根因

`adminAuthService.logout` 当前只调用 `SplitAdminRefreshToken` 得到 `sid`，随后直接撤销该会话，没有验证 Token 摘要。攻击者若获知 `sid`，可构造 `sid.anything` 使对应设备下线。

#### 关联文件

- `internal/app/admin_session.go`
- `internal/app/admin_auth_handlers.go`
- `internal/app/admin_session_test.go`
- `internal/app/admin_auth_http_test.go`
- `internal/storage/admin_sessions.go`
- `internal/security/jwt.go`

#### 修改范围

- 在撤销前读取会话并计算所提交 Refresh Token 的摘要。
- 只有摘要匹配当前 `refresh_hash`，或匹配仍在并发宽限期内的 `previous_refresh_hash`，才允许撤销。
- 比较使用恒定时间方法或现有安全比较工具。
- 无效、格式错误、未知、已撤销、已过期或摘要不匹配的 Token 对客户端保持幂等成功并清 Cookie，但不得撤销任何会话。
- 存储不可用时保持当前客户端可退出的体验；服务端错误必须记录，但不得把未经验证的 `sid` 当成授权依据。
- 不改变 Refresh 重放检测与全会话撤销规则。

#### 正常流程

1. 浏览器携带当前 Refresh Cookie。
2. 服务端解析、加载会话、验证摘要。
3. 单个会话以 `logout` 原因撤销。
4. 返回 `200 {"ok":true}` 并清 Cookie。

#### 失败路径与安全边界

- 伪造相同 `sid`、错误 secret：返回幂等成功，真实会话仍有效。
- 格式错误 Token：返回幂等成功，不查询或不修改无关会话。
- 已轮换的上一代 Token 在宽限期内：允许撤销同一会话。
- 更早 Token 或宽限期外 Token：不得仅凭 `sid` 撤销。
- 不得在错误响应、日志或审计中输出 Refresh Token、摘要或 secret。

#### 回归用例

- 正确 Token 只撤销自身会话，不影响同账号其他设备。
- `sid` 相同但 secret 错误不能撤销。
- 不存在的 `sid`、垃圾字符串、重复 Logout 均幂等。
- 并发轮换后的当前 Token和宽限期内上一代 Token行为符合定义。
- Logout 始终清理浏览器 Cookie。

#### 验收标准

- 不存在仅根据 `sid` 调用 `RevokeAdminSession` 的路径。
- 伪造 Token 的回归测试在旧实现失败，在新实现通过。
- 现有 Refresh 轮换、并发宽限和 replay 测试无回归。

### P1-05 严格 JSON 请求边界

#### 根因

`decodeAdminAuthBody` 使用 `DisallowUnknownFields`，但第一次 `Decode` 成功后未验证 EOF，因此可能接受一个合法对象后跟第二个 JSON 值。

#### 关联文件

- `internal/app/admin_auth_handlers.go`
- `internal/app/admin_auth_http_test.go`
- `internal/app/account_registration_test.go`
- `internal/app/account_mutation_test.go`

#### 修改范围

- 一个请求体只允许一个 JSON 值，之后只能有空白并到达 EOF。
- 保留 `DisallowUnknownFields`。
- 保留请求体大小上限，并确保超限时稳定返回 `400 invalid_request`。
- 不为单个端点复制解码逻辑，继续使用一个高内聚的公共 helper。
- 不改变已公开字段名和成功响应。

#### 正常流程

- 单个合法 JSON 对象加任意尾随空白正常处理。

#### 失败路径与安全边界

- 两个连续 JSON 对象、对象后跟数组、对象后跟标量：`400 invalid_request`。
- 未知字段、语法错误、空请求体、超限请求体：`400 invalid_request`。
- 解码失败不得产生用户、会话、角色变更或审计中的虚假成功事件。

#### 回归用例

- Login、Setup、Register、Password Change、User PATCH 均覆盖第二个 JSON 值。
- 合法尾随空白仍通过。
- 安全字段注入仍被拒绝。
- 无效请求不消耗注册限流预算的现有规则保持不变。

#### 验收标准

- helper 明确执行第二次 Decode 并只接受 `io.EOF`，或采用等价且可测试的实现。
- 所有使用该 helper 的端点行为一致。

### P1-06 Admin UI 浏览器安全头

#### 根因

`serveAdminUI` 当前主要设置 Content-Type 与 Cache-Control，没有 CSP、防嵌入、MIME 嗅探和来源策略。

#### 关联文件

- `internal/app/admin_ui.go`
- 新增或现有的 Admin UI HTTP 测试文件
- `web/admin/src/components/ui/Table.tsx`
- `web/admin/src/components/ui/Detail.tsx`
- `web/admin/src/components/ui/Skeleton.tsx`
- `web/admin/index.html`

#### 修改范围

- 对 `/admin-ui`、静态资源和 SPA fallback 统一设置：
  - `Content-Security-Policy`
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
  - 最小必要的 `Permissions-Policy`
- CSP 默认建议：`default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'; style-src 'self' 'unsafe-inline'`。
- 当前 React 组件使用内联 style 属性，Phase 1 允许仅在 `style-src` 保留 `'unsafe-inline'`；`script-src` 禁止 `'unsafe-inline'` 和 `'unsafe-eval'`。
- 不在无法保证 HTTPS 的应用层强制 HSTS；HSTS 属于生产 TLS 入口配置。
- 不修改数据面 API 的 CORS 或缓存策略。

#### 正常流程

- Admin UI 首页、哈希资源和客户端路由正常加载。
- API 请求仍通过同源 `connect-src 'self'`。

#### 失败路径与安全边界

- 第三方 frame 嵌入被拒绝。
- 未授权外部脚本、对象和跨源连接被 CSP 拒绝。
- 静态文件仍保留 immutable 缓存，`index.html` 仍为 `no-cache`。

#### 回归用例

- 首页、静态资源、SPA fallback 和 404 响应头断言。
- 前端生产构建通过。
- 浏览器加载无 CSP violation、白屏或 API 连接失败。
- favicon 的 `data:` URL仍可加载。

#### 验收标准

- 所有 Admin UI 成功响应具有一致安全头。
- 不通过放宽 `script-src` 解决构建问题。
- 不影响 `/v1/*`、`/healthz`、`/metrics` 和管理 API 的响应体或缓存语义。

### P1-07 注册成功但自动登录失败的可恢复语义

#### 根因

`CreateUser` 与 `openSession` 分为两个步骤。用户已创建后若 Session 创建失败，接口返回通用失败；用户重试注册会得到用户名已占用，无法判断账号是否已经存在。

#### 关联文件

- `internal/app/account_registration.go`
- `internal/app/admin_auth_errors.go`
- `internal/app/admin_session.go`
- `internal/app/account_registration_test.go`
- `web/admin/src/api.ts`
- `web/admin/src/auth/RegisterPage.tsx`
- `web/admin/src/lib/errors.ts`
- `docs/ACCOUNTS.md`

#### 修改范围

- 采用最小修复，不重构跨表大事务，不在失败时补偿删除已经创建的用户。
- 用户创建成功但 Session 创建失败时返回专用错误码，例如 `account_created_login_failed`，HTTP 使用 `503`。
- 响应明确提示账号已创建，可直接登录；不得返回密码、哈希、Token 或内部数据库错误。
- 前端切回登录视图并保留用户名，不保留密码。
- 写入可区分的审计或安全事件，且不把该情况错误记录为完整注册成功。
- 显式开启公开注册时才存在该路径；默认关闭注册不改变此修复要求。

#### 正常流程

- 创建用户、创建 Session、写 Cookie并返回 `201`，现有行为不变。

#### 失败路径与安全边界

- `CreateUser` 失败：保持当前用户名冲突和内部错误语义。
- `CreateUser` 成功、Session 失败：返回专用 `503`，账号保留，用户下一步直接登录。
- 客户端不得自动重发注册请求。
- 审计中不得出现密码、Token 或 Session secret。

#### 回归用例

- fake store 注入 Session 创建失败，断言用户只创建一次。
- 返回专用错误码和状态码。
- 随后使用原凭据登录可以成功。
- 前端不重复注册，不保留密码。
- 正常注册继续返回 `201` 并自动登录。

#### 验收标准

- 用户永远不会因该故障被提示为纯粹的注册失败。
- 不引入用户删除补偿或跨层事务耦合。
- 文档移除该项 technical debt，并记录新契约。

### P1-08 独立数据库迁移命令与部署门禁

#### 根因

`deploy/docker-compose.yml` 仅把 `0001_init.sql` 挂载到 PostgreSQL 初始化目录。该机制只在空 volume 首次初始化时执行，并且不会运行 `0002` 到 `0009`，导致认证表或列缺失。

#### 关联文件

- `migrations/*.sql`
- `go.mod`、`go.sum`，仅在确有必要时修改
- 建议新增 `internal/migrate/`
- 建议新增 `cmd/migrate/main.go`
- `deploy/Dockerfile`
- `deploy/docker-compose.yml`
- `deploy/docker-compose.dev.yml`
- `scripts/setup.ps1`
- `scripts/dev.ps1`
- `README.md`
- `docs/PRODUCTION.md`
- 迁移单元与集成测试

#### 修改范围

- 提供独立 migration 命令，应用 `migrations/` 中的全部版本文件。
- 建立 `schema_migrations` 表，至少记录版本、文件名、校验值和应用时间。
- 每个文件成功提交后才记录；失败必须返回非零状态。
- 已记录迁移的校验值变化必须失败，禁止静默重放被修改的历史文件。
- 对已有数据库且没有 `schema_migrations` 的情况，按顺序重放现有幂等迁移并在成功后建立记录。
- Docker 镜像包含 migration 可执行文件和唯一一份迁移源文件。
- Compose 使用一次性 migrate 服务或等价显式步骤；gateway 只在迁移成功后启动。
- 移除仅执行 `0001` 的双轨初始化方式，避免两种机制产生不同结构。
- 不让 server 进程在每次启动时隐式修改 schema。
- 不修改业务数据，不删除表、列、账号、租户、Key、日志或审计。

#### 正常流程

1. PostgreSQL 就绪。
2. migration 命令连接目标数据库。
3. 按文件名顺序应用未执行迁移。
4. 全部成功后退出 0。
5. gateway 启动并通过健康检查。

#### 失败路径与安全边界

- 数据库不可达、权限不足、SQL 失败、校验值冲突：migration 非零退出，gateway 不启动。
- 两个 migration 实例并发：必须通过 PostgreSQL advisory lock 或等价机制串行化。
- 已完成版本重复执行：无业务数据变化。
- 旧数据库只有 `0001` 结构：安全补齐到最新版本。
- 空数据库：一次完成全部迁移。

#### 回归用例

- 空临时数据库应用 `0001` 到 `0009` 后结构完整。
- 仅有 `0001` 的临时数据库可升级。
- 最新数据库再次运行无变化。
- 故意制造失败时不记录该版本，gateway 启动门禁生效。
- 修改已记录迁移的测试副本后校验失败。
- 并发运行两个 migration 进程时只有一个执行，另一个等待或安全退出。

#### 验收标准

- 新部署不会只得到 `0001` 结构。
- 迁移失败时不存在网关带着半完成 schema 运行的路径。
- 历史迁移保持单一来源，不复制两套 SQL。
- 所有状态性迁移测试只针对独立临时数据库。

### P1-09 全量回归、文档同步和交付报告

#### 关联文件

- 前述所有测试文件
- `README.md`
- `docs/ACCOUNTS.md`
- `docs/SSO_RBAC.md`
- `docs/PRODUCTION.md`
- `docs/ACCOUNT_REGISTRATION_REPORT_2026-10-13.md`
- WorkBuddy 新增的 Phase 1 完成报告

#### 实施步骤

1. 运行格式化、Go 测试、前端类型检查和生产构建。
2. 在明确授权的独立环境运行迁移和状态性 E2E。
3. 对照第 7 节不变量逐项核对。
4. 对照第 8 节测试矩阵保存证据。
5. 更新既有文档中与 Phase 1 行为冲突的描述。
6. 输出变更清单、测试结果、跳过项、残余风险和后续任务。

#### 验收标准

- 文档、实现、默认配置和测试没有互相矛盾。
- 没有未完成占位标记或未解释的跳过项。
- Phase 1 diff 不包含第 4.2 节禁止实现的能力。
- 用户确认前不提交、不推送、不合并、不部署。

## 7. 可检查的不变量与兼容性要求

### 7.1 认证模式

- Phase 1 保留 `session`、`token`、`trusted-header` 三种互斥模式，不增加第四种或混合模式。
- `session` 模式不得读取 `ADMIN_TOKEN` 作为旁路凭据。
- `token` 模式的有效 `ADMIN_TOKEN` 继续等价于 `super_admin`。
- `trusted-header` 必须同时验证代理密钥和用户身份；用户头本身不是可信凭据。
- token 和 trusted-header 模式下 `/auth/register` 继续为 `404`。
- token 和 trusted-header 模式下本地账户管理接口继续安全拒绝，不能解引用空 `adminAuth`。

### 7.2 Setup 与账号

- 首个管理员判定条件仍是是否存在 `super_admin`，不能改回账号表是否为空。
- 首个 `super_admin` 创建继续使用事务和 advisory lock。
- 最后一个启用的 `super_admin` 不可被降权或停用。
- 公开注册永远不能携带或决定角色，固定创建 `member`。
- `member` 与有效全局管理角色互斥。
- 未知角色在显式管理员写入时返回 `400`，不得静默忽略。

### 7.3 Session 与 Token

- Access Token 继续只保存在前端内存，不写入 localStorage、sessionStorage 或 IndexedDB。
- Refresh Token Cookie 继续为 HttpOnly、SameSite=Strict，并保持现有 Path。
- 数据库只保存 Refresh Token 摘要。
- Refresh 并发宽限期内的上一代 Token继续返回同一个后继 Token，不撤销会话。
- 宽限期外的重放继续撤销该账号全部会话并写安全审计。
- 角色、状态或密码变更继续通过 `auth_version` 立即使旧凭据失效。
- 一个账户 PATCH 中不论同时变更多少安全字段，`auth_version` 最多加 1。
- 仅显示名称变化不撤销会话；no-op 不更新 `updated_at`。

### 7.4 注册与限流

- 注册默认关闭，但显式启用后的既有 API 契约保持兼容。
- 注册限流保持单个原子 Redis 操作，禁止拆回先检查后记录。
- 上限 5 时恰好允许 5 次，第 6 次拒绝。
- TTL 只在第一次请求设置，攻击请求不能延长窗口。
- 注册限流 Redis 故障继续 fail closed。
- 登录限流 Redis 故障继续 fail open 并记录日志。
- 登录成功只清理 IP+用户名和用户名计数，不清 IP spray 计数。

### 7.5 RBAC

- `super_admin` 保持全部控制面权限。
- `/admin/users*` 仍仅允许 `super_admin`。
- `gateway_admin` 不能管理用户或为自己提权。
- `operator` 只能读取状态、读取收窄后的模型测试选项并执行模型连通测试。
- `auditor` 只读，不得写控制面资源。
- 无角色或未知角色的 SSO 用户只能成为 `member`。
- 遗留全局 `tenant_admin` 在 Phase 1 不得访问任何租户或 Key 管理接口。

### 7.6 API 与前端

- Phase 1 不删除或改名现有认证路径。
- 认证响应继续设置 `Cache-Control: no-store` 和 `Pragma: no-cache`。
- 错误响应保持统一 envelope 和稳定错误码。
- 前端角色过滤必须与后端允许列表一致，但后端始终是权威边界。
- `App.tsx` 的 hooks 必须位于所有按 phase 提前 return 之前。
- Admin UI 构建产物由既有流程生成；不得手工编辑压缩后的 bundle。

### 7.7 数据面、路由与 Provider

- `/v1/models`、`/v1/chat/completions`、SSE、API Key 鉴权和配额逻辑不得改变。
- Tenant、业务 API Key、模型、Provider、Route 的数据库结构和业务语义不得改变。
- ThinkWatch 同步、故障切换、client allow 生成和遥测解析不得改变。
- Provider 真实密钥不得写入 PostgreSQL、日志、仓库或 Admin UI。
- Phase 1 不修改真实 Provider、模型线路或 ThinkWatch 配置。

### 7.8 数据库迁移

- 迁移必须按版本顺序执行并记录。
- 历史迁移不得被静默改写。
- 迁移失败必须阻止应用启动。
- 迁移过程不得删除或重置现有业务数据。
- 所有 migration 测试使用独立临时数据库。

## 8. Phase 1 测试矩阵

| 范围 | 正常路径 | 异常与安全路径 | 修复有效的判据 |
|---|---|---|---|
| trusted-header | 明确角色保持原权限 | 空角色、未知角色、伪造头 | 空或未知角色不能读取任何全局管理数据 |
| legacy tenant_admin | super_admin 管理租户正常 | tenant_admin 读取或修改任意租户/Key | 所有相关请求为 403，且无数据变化 |
| 注册默认值 | 显式开启后注册成功 | 默认关闭、Setup 未完成、Redis 故障、安全字段注入 | 默认无注册入口；显式开启保持原契约 |
| Logout | 有效当前 Token 撤销自身 | 错 secret、垃圾 Token、旧 Token、重复退出 | 伪造同 sid Token不能撤销真实会话 |
| JSON | 单对象和尾随空白 | 第二 JSON 值、未知字段、超限、空体 | 全部稳定返回 400 且无副作用 |
| Admin UI 头 | 首页、资源、SPA fallback 正常 | frame 嵌入、外部脚本、MIME 嗅探 | 安全头存在，前端无 CSP 错误 |
| 注册部分成功 | 用户和 Session 均成功 | 用户成功但 Session 失败 | 返回专用 503，用户可直接登录，不重复注册 |
| Migration | 空库和旧库升级 | SQL 失败、校验冲突、并发执行 | 失败阻止启动，成功可幂等重跑 |
| Session 回归 | 登录、刷新、退出正常 | replay、并发刷新、停用、改角色 | 原有 Session Auth v2 不变量全部保持 |
| 数据面回归 | models、非流式、SSE 正常 | 无效 API Key、配额拒绝 | HTTP 契约和路由结果不受 Phase 1 影响 |

安全测试必须证明旧漏洞路径被阻断，不能只证明正常路径仍可用。

## 9. 环境安全边界

### 9.1 禁止操作

WorkBuddy 不得在未获得用户逐项授权的情况下执行以下操作：

- 不连接、查询、迁移、清空或重建用户现有 `campus_gateway` 数据库。
- 不连接或清理用户现有 `campus_gateway_test`，除非已确认它是本轮专用且可销毁。
- 不删除或重建现有 PostgreSQL Docker volume。
- 不执行会清理用户 Redis 的 `FLUSHALL`、`FLUSHDB`、通配删除或未知前缀删除。
- 不修改用户 Redis 中的业务限流、配额、并发或 Session 数据。
- 不修改 `%APPDATA%\ThinkWatch\config.yaml`、ThinkWatch 运行配置或现有进程。
- 不调用真实 Qwen、DeepSeek、Ark 或其他收费 Provider。
- 不读取、输出、轮换或覆盖真实 API Key、Session Secret、代理密钥或管理员密码。
- 不修改 `.env`、宿主机环境变量、Secret Manager、KMS、Vault 或反向代理真实配置。
- 不启动、停止、重启或重新配置用户正在使用的 Gateway、PostgreSQL、Redis、Nginx、ThinkWatch 或 oauth2-proxy。
- 不重置现有管理员密码，不创建真实账号，不改变真实角色、租户、Key 或配额。
- 不提交、不推送、不创建或合并 PR，不切换或重置到其他分支。
- 不修改、删除或提交 `.workbuddy-ai/`。

### 9.2 允许的验证方式

- 默认只运行无状态单元测试、静态检查、类型检查和构建。
- 状态性集成测试和 E2E 必须使用独立环境：独立临时数据库、独立 Redis 实例或随机且可证明隔离的命名空间、独立端口和测试凭据。
- 临时资源名称必须包含 Phase 1 和随机后缀，禁止复用用户业务库名。
- 测试脚本必须在开始前打印目标地址和资源名，并拒绝默认生产库名。
- 测试完成后只清理本轮创建且已核对名称的临时资源。
- 若无法证明隔离，跳过状态性测试并在交付报告中标记 `SKIPPED`，不得冒险运行。

## 10. 工程原则

- 第一性原理：先明确需要保护的资产、信任边界和攻击路径，再选择最小修复。
- 最小必要改动：只修改能够关闭已确认风险的代码，不借机重构无关模块。
- 奥卡姆剃刀：优先复用已有角色规范化、Session store、错误 envelope 和测试设施。
- 高内聚低耦合：JSON 解码、迁移、角色解析、Session 所有权校验各自只有一个权威实现。
- 默认拒绝：信息缺失、角色未知或状态不一致时降低权限，不做猜测性授权。
- 后端强制：任何前端隐藏都必须有对应后端权限检查。
- 可审计：安全相关状态变化必须能区分成功、拒绝和内部失败，但日志不得包含凭据。
- 可回滚：Phase 1 不做破坏性 schema 变更，不删除数据，不重命名核心表。
- 不掩盖错误：测试跳过、迁移失败和环境缺失必须如实报告。

## 11. 建议测试命令

以下命令应从仓库根目录运行。状态性命令只有在满足第 9 节隔离要求后才允许执行。

```powershell
go test -count=1 ./...
go vet ./...
npm --prefix web/admin run build
```

若 `web/admin/package.json` 定义了独立类型检查脚本，优先使用该脚本；否则使用项目现有的 TypeScript 构建方式。不得为方便测试而全局安装未知依赖。

可选但必须如实标记环境能力：

```powershell
go test -race ./...
```

当前已知 Windows 环境可能缺少 `gcc`，导致 `-race` 无法运行。此时记录 `SKIPPED: missing C toolchain`，不要伪造结果。

状态性 E2E 不得直接运行现有脚本，除非 WorkBuddy 已先审查其数据库、Redis、端口和进程操作，并将目标改为本轮独立环境。

## 12. 完成定义

WorkBuddy 只有满足全部条件后才能声明 Phase 1 完成：

1. P1-01 到 P1-08 均有对应实现和自动化测试。
2. 每个安全问题都有至少一个在旧实现失败、在新实现通过的回归用例。
3. 第 7 节全部不变量逐项通过或有明确、经用户接受的例外。
4. `go test -count=1 ./...`、`go vet ./...`、前端生产构建通过。
5. 状态性迁移与 E2E 已在独立环境通过，或因环境限制明确标记 `SKIPPED`。
6. Git diff 只包含 Phase 1 相关实现、测试和文档，不含 `.workbuddy-ai/`、凭据、运行产物或后续阶段代码。
7. 没有真实密码、Token、Key、Cookie、数据库连接串或用户数据进入日志和提交内容。
8. 没有修改真实数据库、Redis、ThinkWatch 配置或外部服务。
9. 现有数据面、模型路由、Provider 和 API Key 行为无回归。
10. 文档已同步，且不存在未完成占位标记或模糊的未来承诺。
11. WorkBuddy 未提交、推送或合并，等待用户和 Codex 独立审查。

## 13. WorkBuddy 交付要求

WorkBuddy 完成开发后，应向用户和 Codex 提供：

- 修改文件清单及每个文件的修改目的
- P1-01 到 P1-08 的完成映射
- 安全问题修复前后的行为对比
- 全部测试命令、退出码和关键结果
- 所有跳过测试及原因
- 是否使用临时数据库或 Redis；若使用，给出隔离方式和清理结果
- Git 状态和 diff 摘要
- 兼容性核对结果
- 未解决风险和后续阶段依赖
- 明确声明未提交、未推送、未合并、未部署

Codex 将基于本交接执行独立审查，重点检查越权路径、Session 所有权、默认配置、迁移门禁、测试真实性和 Phase 1 范围控制。

## 14. 后续阶段规划

以下任务已经确认，但禁止在 Phase 1 提前实现。

### Phase 2：身份与租户授权基础

依赖 Phase 1 合并并稳定后开始：

- SSO 与本地应急入口共存的混合认证边界
- `issuer + subject` 身份表和 SSO JIT 自动开户
- 个人租户自动创建
- 用户与租户多对多成员关系
- `idp/local` 成员来源和邀请确认
- 租户内 `tenant_admin/developer/viewer`
- 全局 `tenant_operator`
- 所有租户资源的作用域查询和跨租户 404
- 现有本地账号到 SSO 的人工关联与 30 天迁移期
- 用户列表分页与检索

### Phase 3：用户调用能力

依赖 Phase 2 的身份和成员关系：

- 用户门户
- 个人与组织 API Key 自助管理
- `created_by_user_id`、服务账号及负责人
- Key 数量、有效期、轮换和到期通知
- 租户、成员、Key 三层配额
- 模型权限模板、高成本模型审批
- 个人和租户范围内的调用日志

### Phase 4：生命周期、合规与运营

依赖 Phase 2 和 Phase 3：

- `active/suspended/deprovisioned`
- 离校回收和组织 Key 接管
- TOTP、恢复码、双人审批和后续 WebAuthn
- 租户申请、配额审批和组织系统同步
- 邮件与后台安全通知
- 日志保留、会话清理、PII 脱敏和数据匿名化
- 故障排查内容的短期开关与 7 天自动删除
- 旧认证接口弃用和 `/auth/*` 统一

任何后续阶段需求若与本文基线冲突，必须先由用户重新确认设计，不得由实施 Agent自行推断。

## 15. Codex 独立审查入口

WorkBuddy 完成后，Codex 应从以下顺序开始审查：

1. `git status --short --branch` 与完整 diff，确认没有越界文件。
2. P1-01 与 P1-02 的后端拒绝逻辑，防止前端隐藏替代授权。
3. P1-04 的 Token 摘要校验，重点构造相同 `sid`、不同 secret。
4. P1-08 的空库、旧库、失败门禁、校验冲突和并发执行。
5. Session Auth v2、注册限流和 last-super-admin 不变量。
6. `/v1` 数据面与 ThinkWatch 相关代码是否出现无关变更。
7. 测试结果与环境隔离证据是否真实完整。

审查通过前，PR #22 不得合并。
