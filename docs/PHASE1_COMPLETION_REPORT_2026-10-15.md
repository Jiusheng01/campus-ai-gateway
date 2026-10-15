# Phase 1 安全加固交付报告

日期：2026-10-15
分支：`feature/user-account-registration`（基线 `ca9ec4a`，PR #22 未合并）
交接文档：`docs/CODEX_HANDOFF_PHASE1.md`（Codex Grilling 后确认的设计决策）
实施：WorkBuddy

> **最新状态**：本文档含两部分——Phase 1 交付（第 1–9 节）与 Codex 独立审查修复轮
> （文末附节，含 P1-04 宽限期 30s→5s 的已确认实施）。当前以附节为准：
> 已提交到 `feature/user-account-registration`，未合并、未部署，等待第二轮审查。

**未提交、未推送、未合并、未部署。** 全部改动仅存在于工作区，等用户与 Codex 独立审查。

---

## 1. 任务完成映射

| 任务 | 内容 | 状态 |
|---|---|---|
| P1-00 | 基线保护与测试清单 | ✅ 基线核对：分支 / HEAD / 工作区与交接文档一致；测试套件全绿后开工 |
| P1-01 | trusted-header 最小权限回退 | ✅ 空 / 未知 / legacy `tenant_admin` 组 → `member`，取消 auditor 兜底 |
| P1-02 | 停止未作用域化 tenant_admin 全局授权 | ✅ 后端 fail closed（403 / 授予 400），前端从可授予列表与导航移除 |
| P1-03 | 公开注册改为显式启用 | ✅ 默认 `false`，契约不变，入口与文档同步 |
| P1-04 | logout 校验 Refresh Token 所有权 | ✅ 撤销前校验摘要（当前代或宽限期内上一代），恒定时间比较 |
| P1-05 | 严格 JSON 请求边界 | ✅ 公共 helper 二次 Decode 只接受 EOF，5 个端点全覆盖 |
| P1-06 | Admin UI 浏览器安全头 | ✅ CSP / nosniff / DENY / no-referrer / Permissions-Policy 全覆盖 |
| P1-07 | 注册成功但登录失败的可恢复语义 | ✅ `503 account_created_login_failed`，账号保留，前端回登录保留用户名 |
| P1-08 | 独立迁移命令与部署门禁 | ✅ `internal/migrate` + `cmd/migrate`，compose 一次性服务门禁 |
| P1-09 | 全量回归、文档同步、本报告 | ✅ 见第 5–9 节 |

第二阶段（租户成员关系、个人 API Key、配额、SSO JIT、TOTP、分页、表重命名）**未实施**。

---

## 2. 修改文件清单与目的

### 后端实现

| 文件 | 目的 |
|---|---|
| `internal/app/account_roles.go` | P1-01/P1-02：`tenant_admin` 移出 `managementRoles`，新增 `unassignableRoles`；`IsManagementRole` 改为查集合；`ValidateAccountRoles` 拒绝不可授予角色（400） |
| `internal/app/admin_auth.go` | P1-01：`parseAdminRoles` 重写（丢弃无特权组、归一化）；`authenticateAdminTrustedHeader` 移除 auditor 兜底；`can()` 移除 tenant_admin 分支并注明原因 |
| `internal/app/admin_session.go` | P1-04：`logout` 重写为「加载会话 → 校验摘要 → 撤销」；新增 `logoutTokenOwnsSession` |
| `internal/app/admin_auth_handlers.go` | P1-05：`decodeAdminAuthBody` 一次 JSON 值 + EOF 校验 + 超限拒绝 |
| `internal/app/admin_auth_errors.go` | P1-07：`ErrAdminAccountCreatedLoginFailed`（503 `account_created_login_failed`） |
| `internal/app/account_registration.go` | P1-03/P1-07：openSession 失败映射为专用 sentinel；审计动作提取为纯函数 `registrationAuditAction`；部分失败写可区分安全事件 |
| `internal/app/config.go` | P1-03：`PUBLIC_REGISTRATION_ENABLED` 默认 `false` |
| `internal/app/admin_ui.go` | P1-06：`setAdminUISecurityHeaders` + CSP 常量，覆盖全部 Admin UI 响应 |
| `internal/migrate/migrate.go`（新增） | P1-08：迁移加载 / 校验 / advisory lock / 每文件单事务 / `schema_migrations` |
| `cmd/migrate/main.go`（新增） | P1-08：迁移命令入口，非零退出门禁 |

### 测试

| 文件 | 目的 |
|---|---|
| `internal/app/admin_auth_test.go` | P1-01/P1-02：角色解析表驱动、`can()` 矩阵、trusted-header 三组新用例（无角色→member+全 403、显式角色不变、伪造身份 401/503） |
| `internal/app/account_registration_test.go` | P1-03/P1-07：默认关闭的 auth-config、关闭时不建号不耗配额、部分失败 503 与审计区分、旧 auditor 兜底测试改写为新契约 |
| `internal/app/config_test.go`（新增） | P1-03：默认 false、各显式值、非 session 模式不可注册 |
| `internal/app/admin_session_test.go` | P1-04：伪造 Token 不撤销、未知/死 Token 无害、宽限期内上一代可撤销、宽限期外不可、存储故障不撤销；fake store 增加 `findSessionErr` / `createSessionErr` 注入 |
| `internal/app/admin_auth_http_test.go` | P1-04 HTTP 层伪造 Cookie；P1-05 五个端点 + helper 单测（第二 JSON 值、尾随空白、超限） |
| `internal/app/admin_ui_test.go`（新增） | P1-06：首页 / 资源 / SPA fallback / 404 头部断言、缓存语义、外来路径、CSP 严格性 |
| `internal/migrate/migrate_test.go`（新增） | P1-08：加载排序/校验/缺口单测；5 个隔离库集成测试（空库、幂等重跑、校验冲突、失败不记录、旧库引导、并发串行） |

### 前端

| 文件 | 目的 |
|---|---|
| `web/admin/src/lib/roles.ts` | P1-02：`ASSIGNABLE_ROLES` 移除 `tenant_admin`；`isManagementRole` 与后端对齐 |
| `web/admin/src/app/navigation.ts` | P1-02：`MANAGEMENT` 与租户 / API Key 导航项移除 `tenant_admin` |
| `web/admin/src/pages/DashboardPage.tsx` | P1-02：注释与后端允许列表一致 |
| `web/admin/src/auth/RegisterPage.tsx` | P1-07：识别 `account_created_login_failed`，丢弃密码、交还用户名，不自动重发 |
| `web/admin/src/auth/LoginPage.tsx` | P1-07：承接回调，预填用户名、切回登录、banner 提示 |
| `web/admin/src/lib/errors.ts` | P1-07：新增错误码中文文案 |

### 部署与文档

| 文件 | 目的 |
|---|---|
| `deploy/Dockerfile` | P1-08：构建 migrate 二进制，镜像内唯一一份 `/migrations` |
| `deploy/docker-compose.yml` | P1-08：移除 `docker-entrypoint-initdb.d` 双轨装载；新增一次性 `migrate` 服务；网关 `service_completed_successfully` 门禁 + postgres 健康检查 |
| `scripts/dev.ps1` | P1-08：psql 逐文件循环改为 `go run ./cmd/migrate` |
| `scripts/e2e-admin-auth-v2.sh` | P1-02：tenant_admin 断言 400→403（脚本仍指向真实库，**未运行**，见第 7 节） |
| `.env.example` / `README.md` / `docs/ACCOUNTS.md` | P1-03/P1-07：默认关闭、注册接口可用性契约、部分失败契约、角色树同步 |
| `docs/SSO_RBAC.md` / `docs/PRODUCTION.md` | P1-01/02/03/07/08：角色语义、错误语义表、迁移门禁 |
| `docs/ACCOUNT_REGISTRATION_REPORT_2026-10-13.md` | P1-09：追加「后续修订」说明块，历史记录不改写 |
| `internal/app/adminui/dist/index.html` | 前端生产构建产物（既有流程重建，引用新哈希资源） |

---

## 3. 安全问题修复前后对比

| # | 问题 | 修复前 | 修复后 |
|---|---|---|---|
| 1 | trusted-header 无角色兜底 | 空/未知组 → `auditor`，可读请求日志、管理审计、安全事件 | → `member`，仅自助端点；whoami 可达 |
| 2 | 全局 tenant_admin | 可读写全部租户与 API Key（无资源范围） | 无任何管理权限（403）；授予返回 400；存量值可显示、可被替换 |
| 3 | 公开注册默认开放 | 未配置即开放匿名密码注册 | 默认关闭；`403 registration_disabled`（不建号、不耗配额）；显式开启契约不变 |
| 4 | logout 仅凭 sid | 知悉 sid 即可构造 `<sid>.anything` 踢下线 | 必须匹配 `refresh_hash` 或宽限期内 `previous_refresh_hash`（恒定时间）；伪造 → 幂等成功但不撤销 |
| 5 | JSON 请求边界 | `{"a":1}{"b":2}` 接受首个对象、静默丢弃第二个 | 一律 `400 invalid_request`；尾随空白放行；超限拒绝 |
| 6 | Admin UI 无安全头 | 仅 Content-Type / Cache-Control | CSP（script-src 禁 inline/eval）、nosniff、DENY、no-referrer、Permissions-Policy，含 404 |
| 7 | 注册部分成功语义 | 通用 500，用户重试得到 username_taken，账号不可达 | `503 account_created_login_failed`，账号保留可直接登录；审计三态可区分 |
| 8 | 迁移双轨且只跑 0001 | compose 只挂载 `0001_init.sql` 到 initdb.d，空卷才执行 | `cmd/migrate` 统一应用全部版本 + 记录 + 校验值 + advisory lock；compose 服务门禁 |

每个问题均配有「旧实现失败、新实现通过」的回归用例（TDD：先 RED 后 GREEN，RED 输出见第 5 节）。

---

## 4. 兼容性核对（交接文档 §7）

- **§7.1 认证模式**：三模式保留；session 不读 `ADMIN_TOKEN`；token 模式 `ADMIN_TOKEN` 仍等价 super_admin；trusted-header 仍同时验证代理密钥与用户头；token / trusted-header 下 `/auth/register` 仍 404（既有测试通过）。
- **§7.2 Setup 与账号**：首管理员判定仍为「存在 super_admin」；事务 + advisory lock 保留；last-super-admin 保护未动；注册固定 `member`；member 与管理角色互斥保留；未知角色显式写入仍 400。
- **§7.3 Session 与 Token**：Access Token 仅内存；Refresh Cookie HttpOnly + SameSite=Strict + Path 不变；库内仅存摘要；并发宽限与 replay 规则未动（P1-04 仅收紧 logout 的所有权校验）；`auth_version` 语义不变。
- **§7.4 注册与限流**：原子单次 Consume、上限 5 恰好放行 5、TTL 只设一次、Redis 故障 fail closed、登录 fail-open、成功只清 pair + username——全部既有测试通过；关闭时不消耗配额为新增断言。
- **§7.5 RBAC**：super_admin 全权；`/admin/users*` 仅 super_admin；gateway_admin 不可提权；operator 收敛不变；auditor 只读；无角色 SSO → member；legacy tenant_admin 全接口 403（新增断言）。
- **§7.6 API 与前端**：未删除/改名认证路径；no-store/no-cache 保留；错误 envelope 与稳定码保留；前端角色过滤与后端一致且后端仍是边界；`App.tsx` hooks 位置未动；未手工编辑 bundle。
- **§7.7 数据面**：`/v1/*`、SSE、API Key、配额、ThinkWatch 同步、遥测解析零改动（无相关 diff）。
- **§7.8 迁移**：版本顺序 + 记录、历史不可静默改写（校验值失败）、失败阻止启动（compose 门禁）、不删数据、测试全部使用独立临时库。

---

## 5. 测试证据

### 命令与退出码

```text
gofmt -l <本轮所有改动文件>            → 无输出（clean）
go vet ./...                          → 通过（exit 0）
go test -count=1 ./...                → 通过：app 187 PASS/0 SKIP、migrate 11 PASS、
                                        storage 9 PASS、proxy / ratelimit / security 全 PASS（exit 0）
go test -race ./...                   → SKIPPED：缺少 C 工具链（`-race requires cgo`），未伪造结果
npm --prefix web/admin run check      → 通过（tsc -b，exit 0）
npm --prefix web/admin run build      → 通过（vite build，exit 0）
docker compose config（dev 合并）      → 通过（exit 0）
docker build --check                  → Dockerfile 解析通过；镜像元数据拉取被本机代理拦截（Bad Gateway），
                                        未做完整镜像构建
```

### RED→GREEN 记录（节选）

```text
P1-01  TestTrustedHeaderWithoutRolesIsOnlyAMember：旧实现空组 → auditor，读取 /admin/request-logs 200（应 403）
P1-02  TestAdminPrincipalPermissions{"tenant_admin", PUT, /admin/tenants/x}：旧实现 true（应 false）
P1-03  TestPublicRegistrationDefaultsToOff：旧默认 true
P1-04  TestLogoutWithForgedTokenDoesNotRevoke：旧实现 0 still live（伪造 Token 撤销了全部会话）
P1-05  TestAuthEndpointsRejectSecondJSONValue：login 200 / register 201 / password 200 / patch 200 / setup 201（均应 400）
P1-06  TestAdminUISetsSecurityHeadersOnEveryResponse：五个头全部缺失
P1-07  TestRegisterAccountCreatedButSessionFailed：旧实现 500 admin_internal_error（应 503 account_created_login_failed）
```

### 隔离与清理

- 迁移集成测试使用 `campus_migrate_phase1_<随机后缀>` 临时库，运行前打印目标名，拒绝业务库名，`t.Cleanup` 中 `DROP ... WITH (FORCE)`；本轮共创建 5 个临时库并全部清理。
- 未连接、查询、迁移、清空或重建 `campus_gateway`；未执行任何 `FLUSHALL` / 通配删除；未修改 Redis 业务数据、ThinkWatch 配置或进程。
- `campus_gateway_test` 仅被既有 `internal/storage` 集成测试按其既有机制使用（drop/recreate 后应用真实迁移），本轮未改动该机制。

---

## 6. 跳过项与未覆盖项

| 项 | 原因 |
|---|---|
| `go test -race ./...` | Windows 本机无 C 工具链，`-race` 需要 cgo。记 `SKIPPED: missing C toolchain` |
| Docker 镜像完整构建 | 本机代理导致 registry 元数据拉取失败（Bad Gateway）；已用 `docker build --check` 解析 Dockerfile、本地 `go build ./cmd/migrate` 与 compose 校验替代 |
| `scripts/e2e-admin-auth-v2.sh` | 该脚本对真实 `campus_gateway` 库执行迁移，违反 §9.1；本轮**未运行**。已按新契约修正其中 tenant_admin 断言（400→403），其余留待后续在隔离环境重写 |
| 浏览器实测（CSP violation / frame 嵌入） | 头部由 HTTP 测试逐项断言；真实浏览器加载留待部署验收 |
| CI 中 12 个集成测试 SKIP | 既有现象（CI 无 postgres/redis service、不传 TEST_DATABASE_URL），非本轮引入；本轮未改 CI 配置 |

---

## 7. 残余风险与后续依赖

1. **存量 `tenant_admin` 账号**：数据库中的全局 `tenant_admin` 不被自动迁移（设计如此），持有者等同 member，需要 super_admin 显式改角色。运维侧无自动告警，建议后续版本在用户列表加「遗留角色」提示。
2. **`adminui/dist` 资产哈希**：前端源码已改，构建产物已重建（index.html 引用新哈希）；提交时需包含该 index.html，否则嵌入的是旧 UI。
3. **e2e 脚本重建**：`scripts/e2e-admin-auth-v2.sh` 仍需整体改造为独立临时库 / 独立 Redis 命名空间后才能纳入常规验证。
4. **CI 集成测试覆盖**：建议为 CI 增加 postgres/redis service，使 12 个集成测试（含本轮新增的迁移测试）在 CI 真实执行。
5. **`gofmt -l` 历史噪音**：工作区中一批未改动文件仍是早期会话留下的 CRLF（git 因 `core.autocrlf=true` 提交时归一化，diff 不受影响），建议后续用 `git add --renormalize .` 统一。

---

## 8. Git 状态

```text
分支：feature/user-account-registration（HEAD ca9ec4a，与交接文档基线一致）
改动：29 个已跟踪文件修改（+1262 / -114）
新增（未跟踪）：internal/migrate/、cmd/migrate/、internal/app/admin_ui_test.go、internal/app/config_test.go
未跟踪且不纳入提交：.workbuddy-ai/（§9.1 禁止）、docs/CODEX_HANDOFF_PHASE1.md（交接文档本身，由用户处置）
```

未 `git add`、未 commit、未 push、未创建/合并 PR、未切换分支。

---

## 9. 结论

P1-01 至 P1-08 全部完成并配有自动化回归；§7 不变量逐项通过；`go test -count=1 ./...`、`go vet ./...`、前端 check/build 全绿；`-race` 与完整镜像构建因环境限制如实标记。等待用户确认与 Codex 独立审查（重点：越权路径、Session 所有权、默认配置、迁移门禁、测试真实性与 Phase 1 范围控制）。

> 本节为 Phase 1 交付时点的结论。其后 Codex 完成独立审查（P0×0 / P1×5 / P2×4），
> 全部问题已修复并经用户确认实施 P1-04（宽限期 30s→5s）——见文末「Codex 独立审查
> 修复轮」附节，该附节是当前状态。

---

# 附：Codex 独立审查修复轮（2026-10-15 下午）

审查结论：P0 × 0、P1 × 5、P2 × 4。本节按问题记录根因、实际修改、失败复现、修复测试、执行证据与剩余风险。P1-01/02/03/05 与 P2-1..4 已全部实施；**P1-04 宽限期经用户确认后实施（30s → 5s）**，见本节第 5 条。

## 1. P1-01 Docker Compose 迁移入口

**根因**：镜像 `ENTRYPOINT` 固定为 `/usr/local/bin/campus-ai-gateway`。Compose 的 `command`
只覆盖镜像 CMD，因此 migrate 容器实际执行的是
`/usr/local/bin/campus-ai-gateway /usr/local/bin/campus-ai-gateway-migrate -migrations /migrations`
——Gateway 主程序带着 migrate 的参数启动，迁移从未执行，而 compose 层面看不出异常。

**修改**：`deploy/docker-compose.yml` 的 migrate 服务增加
`entrypoint: ["/usr/local/bin/campus-ai-gateway-migrate"]`（`command` 保留参数），
并注释说明为何必须覆盖 entrypoint。

**复现（旧写法）**：固定 entrypoint 的容器收到 command 作为参数并忽略：
`IMAGE-ENTRYPOINT (gateway binary) ran, ignoring args: /usr/local/bin/campus-ai-gateway-migrate -migrations /migrations`。

**验证（真实运行，非仅 config）**：独立 Compose 项目 `phase1_review_p1_01` + 临时卷 +
临时库 `campus_gateway_p1review`，用本地镜像与交叉编译的迁移二进制复刻修复后的
entrypoint/command 语义（完整镜像构建因本机代理无法拉取 golang/alpine 基础镜像，标记
NOT VERIFIED）：

```text
成功路径（全新卷）：
  migrate: bootstrapped bookkeeping; 9 migration(s) verified up to date   → exit 0
  gateway-gate: schema_migrations rows=9 → verified, gateway would now serve traffic  → exit 0
幂等路径（第二次运行）：migrate: database is up to date (9 migration(s) recorded) → exit 0
失败路径（DATABASE_URL 指向不存在的库）：
  migrate: connect: ... database "campus_gateway_p1review_missing" does not exist → exit 1
  gateway-gate 从未启动（无 Starting/Started 记录），compose 整体 exit 1
```

验证环境已拆除（容器 0、卷 0、目录已删）。`docker compose config` 校验通过。

## 2. P1-02 JSON 请求体无限 drain

**根因**：`decodeAdminAuthBody` 的 `defer` 中 `io.Copy(io.Discard, r.Body)` 无界读取剩余
请求体。请求体超限被拒绝后，客户端持续流式发送即可让 handler 永远阻塞在 drain 上——
每个请求消耗一个 goroutine 和一个连接。

**修改**：`internal/app/admin_auth_handlers.go` —— 删除无界 drain。正常路径（≤4KB）的
body 已被 `io.ReadAll(io.LimitReader(...))` 完整读取，无需 drain；超限路径直接拒绝，
net/http 在 handler 返回且 body 未读尽时关闭连接（放弃 keep-alive），这是拒绝请求的正确
代价。读取总量恒 ≤ `adminAuthBodyLimit+1`。

**复现（旧实现）**：两个场景均无限阻塞（测试 10s 超时失败）：
`valid value then endless stream`、`endless stream from the start`。

**测试**：`TestDecodeAdminAuthBodyNeverReadsForever`（无限 Reader × 2，
`endlessAfterPrefix` / `endlessReader`，goroutine + 10s 超时断言）；
`TestDecodeAdminAuthBodyBoundsWhatItReads`（小 body 仍正常解码）。

**残余风险**：客户端以极低速率发送 ≤4KB 仍可拖住读取——这属于传输层慢速攻击，
任何读取 body 的 handler 都无法在应用层单独消除；按审查要求未引入全局超时
（会波及其它 API / SSE / 大文件语义），建议在边缘 TLS / 反代层配置连接级 deadline。

## 3. P1-03 旧库迁移安全

**根因**：无 `schema_migrations` 的旧库走 bootstrap 路径时，重放全部迁移后**未经验证**
就把全部版本记为已应用。重放是幂等的，但“语句没报错”不等于“schema 到达了终态”：
一张被手工建坏的表（存在但形状不对）会让 `CREATE TABLE IF NOT EXISTS` 静默 no-op，
随后整个历史被记为完成。

**修改**（`internal/migrate/baseline.go` 新增 + `Apply` 重构）：
- 区分空库与历史库：`hasApplicationTables` 检查业务表是否存在。
- bootstrap 路径改为「无记录重放 → 终态基线验证 → 单事务记录全部」；
  验证不过则 fail closed，不写任何历史。
- 基线 = 每个迁移一个表（0001–0008 共 10 张）+ 关键列标记
  （`gateway_request_log.{upstream_model,failover}`、`gateway_tenant.{code,name,daily_token_limit}`、
  `gateway_admin_user.{username,auth_version}`、`gateway_admin_session.previous_refresh_hash`）。
- 新增 `ErrFilenameMismatch`（见 P2-3）。未修改任何已发布迁移文件。

**测试**（全部隔离临时库，跑前打印目标名，t.Cleanup 清理）：
`TestApplyBootstrapsMidHistoryLegacyDatabase`（0001–0005 裸应用后引导）、
`TestApplyFailsClosedOnDriftedLegacyDatabase`（残缺 `gateway_tenant`：重放全部成功但
基线缺列 → 错误含 "baseline" 与 "gateway_tenant.code"，0 条记录）、
`TestBootstrapRecordsNothingWhenAFileFails`（bootstrap 失败零记录）、
既有并发（advisory lock）、失败恢复、校验冲突测试保持不变。

**残余风险**：基线是存在性检查而非完整 schema diff——列类型被人为改错的漂移检测不到；
已在 baseline.go 注释中说明维护规则（新增表/关键列时同步扩展）。

## 4. P2-1..P2-4

| # | 问题 | 根因 | 修改 | 测试 |
|---|---|---|---|---|
| P2-1 | 管理员统计与新角色语义不一致 | `AdminUserStats` 的 SQL 谓词仍含 `tenant_admin`，遗留账号被计为管理员 | 提取 `managementRolePredicate` 常量并移除 `tenant_admin`，注释指明 `managementRoles` 为唯一事实来源 | `TestAdminUserStatsCountsRolesTheWayTheGatewayDoes`（RED: members=1 want 2） |
| P2-2 | 伪造 logout 未撤销却被审计为成功登出 | handler 无条件写 `admin_logout` 200 | `logout` 返回 `(revoked, err)`；新增纯函数 `logoutAuditAction`，未撤销写 `admin_logout_rejected` | `TestLogoutAuditAction` + 现有 logout 用例补 `revoked` 断言（伪造=false、真实=true、未知=false） |
| P2-3 | `schema_migrations` 历史文件名未参与一致性校验 | `checkHistory` 只比版本与校验值，改名可保持校验值不变 | 增加文件名比对，失败返回新 sentinel `ErrFilenameMismatch` | `TestCheckHistoryDetectsFilenameMismatch`（RED 实证：临时回退后 `expected ErrFilenameMismatch, got <nil>`） |
| P2-4 | 注册部分成功可能残留孤立会话 | `openSession` 提交 session 行后 `mintAccessToken` 失败会留下无人持有 Token 的活会话 | `openSession` 在 mint 失败时以新原因 `signin_failed` 撤销该会话；`finishRefresh` 的 mint 失败**不**撤销（轮换已提交，客户端凭上一代 Token 在宽限期内重试可恢复，撤销会把可恢复的竞态变成强制重登）；新增 `mint` 字段作测试缝 | `TestRegisterDoesNotLeaveAnOrphanedSession` + `TestRefreshMintFailureKeepsTheSession` |

**P2-4 触发条件分析**（审查要求“先分析”）：
- 唯一可达路径是 mint 失败且发生在 session INSERT 提交之后；`CreateAdminSession` 失败
  不会留行。mint（HS256 签名）失败要求密钥材料异常，现实中几乎不可达，但修复是防御性的。
- 影响：一张活到 refresh TTL 过期的会话行，持有者从未收到 Token，用户设备列表里
  出现一台“从未存在”的设备。不可被利用（无人持有 Token），但属不可解释状态。
- refresh 路径的 mint 失败不撤销的理由见上表——这是两处失败语义不同的原因。

## 5. P1-04 Refresh 宽限期：已确认实施（30s → 5s）

用户确认采用评估节建议的方案 C：**只改默认值，不动机制**。

**修改范围（最小）**：
- `internal/app/config.go`：新增包级常量 `adminRefreshReplayGraceDefault = 5 * time.Second`
  （含“这个值做什么、不做什么”的注释），`ADMIN_REFRESH_REPLAY_GRACE` 的 fallback 由
  `30*time.Second` 改为该常量。显式环境变量仍然优先；`>0` 校验与既有取值语义不变
  （`0`/`0s`/负数/无法解析 → 回落到 5s，不会以零宽限进入生产）。
- **未改**：deterministic successor、`previous_refresh_hash`、`previous_refresh_valid_until`、
  `replayDetected`、session family 撤销、Refresh Token 格式、Cookie、Access Token、`auth_version`。
- 文档/示例：`.env.example`（5s + 生产建议）、`README.md`、`docs/SSO_RBAC.md` §1.5
  （边界语义、不做什么、单飞刷新的正确表述、生产建议）。

**测试（可注入时钟，无真实 sleep）**：

| 场景 | 结果 |
|---|---|
| 未配置 `ADMIN_REFRESH_REPLAY_GRACE` | **PASS** → 5s（`TestRefreshReplayGraceDefaultAndOverride`） |
| 显式 `10s` / `1m` / `30s` | **PASS** → 按显式值；`0s`/负数/乱值 → 回落 5s |
| T0: A→B；T0+4s 再用 A | **PASS** → 良性并发：返回**同一个后继 B**，会话仍 active，无 replay、无撤销（`TestRefreshSupersededTokenWithinFiveSecondsIsBenign`） |
| 边界：T0+5s 正好 / T0+5s+1ns 再用 A | **PASS** → 正好仍良性（延续 `now <= validUntil` 语义，未改边界定义）；+1ns 判 replay 并撤销全家族（`TestRefreshSupersededTokenAtFiveSecondBoundary`） |
| T0+5s+ε 再用 A | **PASS** → `ErrAdminRefreshReplay` + `RevokeSessionsForUser`，同账号其他设备一并撤销，原因 `refresh_reuse_detected`，合法轮换签发的 Token 同样失效（`TestRefreshSupersededTokenAfterFiveSecondsIsReplay`） |
| 并发刷新（输家读到轮换前数据行） | **PASS** → 两个请求收敛到同一后继，零撤销（`TestConcurrentRefreshWithinFiveSecondsConverges`）；既有 `TestRefreshReuseWithinGraceIsBenign` 回归通过 |
| 当代 Token 不受宽限影响 | **PASS** → T0+30s（6 个宽限期）用 B 仍正常 B→C→D；更早一代（B 在 C→D 后出示）判 replay（`TestRefreshCurrentTokenUnaffectedByFiveSecondGrace`） |
| 连续正常轮换 A→B→C→D | **PASS** → previous hash 只代表最近上一代 |

**必须同时说清的话**（注释、README、SSO_RBAC、本报告一致）：
5s grace 解决的是「合法并发刷新时，刚被轮换掉的 previous refresh token 仍可在极短时间内被
识别为 benign race」。缩短 30s → 5s，减少的是**上一代 Token 可被接受的宽限时间**。
它**不能**防止当前有效 Refresh Token 被盗，也**不能替代** HttpOnly、Secure、SameSite、
refresh rotation、replay detection 与 session family revocation。禁止表述为
「5 秒即可消除 Refresh Token replay 风险」——本报告与文档均未这样写。

**单飞刷新的正确表述**（按用户要求修正）：前端 module 级 single-flight 主要避免**同一 JS
runtime 内**的重复 refresh；真正的跨 tab / 跨 runtime 并发安全，仍主要依赖后端的
deterministic successor + previous hash + replay grace。二者互补，不互相替代。

**生产建议**（已写入 SSO_RBAC）：默认 5s；高延迟网络 / 特殊反代 / 跨区域控制台可谨慎 10s；
不建议恢复 30s（除非有明确实测需求）；不要设 0s（正常并发竞态会被误判为 replay）。

## 6. 本轮测试与执行证据

```text
go test -count=1 ./internal/app/ -run "Refresh|Replay|Session|Auth" → 全绿（含 P1-04 全部新用例）
go test -count=1 ./...    → 全绿（app 193+7 PASS/0 SKIP；migrate 15 项 = 7 单元 + 8 隔离库集成；
                            storage 10 项含 P2-1 统计集成测试）
go vet ./...              → 通过
npm --prefix web/admin run check / run build → 通过
docker compose config（deploy 两文件合并）    → 通过
P1-01 真实运行（独立项目/临时库/临时卷）       → 成功、幂等、失败三路径均符合预期
go test -race             → SKIPPED：本机无 cgo
完整 Docker 镜像构建       → NOT VERIFIED：本机代理无法拉取基础镜像（Bad Gateway）
```

**本报告随本轮修改一同提交到 `feature/user-account-registration`，等待 Codex 第二轮独立审查。
未合并、未部署。** 复审重点：compose 入口、有界 body、迁移基线与 5s 宽限、恒定时间比较、孤立会话、登出审计、文件名校验、统计修正、是否引入无关 diff。

## 7. 最终工作区状态与自查记录

```text
git diff：31 个已跟踪文件（+1919 / -144）= Phase 1 交付的 29 个 + 本轮新增的
          internal/storage/admin_users.go、internal/storage/admin_users_integration_test.go
未跟踪并随提交纳入：cmd/migrate/、internal/migrate/（含 baseline.go）、
          internal/app/admin_ui_test.go、internal/app/config_test.go、
          docs/PHASE1_COMPLETION_REPORT_2026-10-15.md、docs/CODEX_HANDOFF_PHASE1.md
不纳入提交：.workbuddy-ai/（§9.1 禁止）
分支：feature/user-account-registration @ ca9ec4a
提交：fix(auth): harden refresh replay grace and phase1 findings（本轮唯一提交，未 merge）
```

**自查记录（对抗性复查中发现并已纠正的自伤）**：本轮曾对 `internal/app/` 整目录执行
`gofmt -w`，把 10 个未参与本轮修改的文件的对齐也重排了（admin.go、server.go 等）。
已用 `git checkout HEAD --` 完整恢复，恢复后重新跑过 `go vet` 与全量测试确认无影响。
教训：`gofmt -w` 只允许作用于本轮显式列出的文件。

---

# 附二：Codex 独立审查第二轮 P1 阻塞项修复（2026-10-15 晚）

本轮修复 Codex 第二轮独立审查提出的四个 P1 Blocking Finding。范围严格限定在四个
发现本身，未引入任何范围外改动（未动用户分页、未加验证码/邮件/密码重置、未改
`PUBLIC_REGISTRATION_ENABLED` 默认值、未改 `tenant_admin` 语义、未改 trusted-header 兜底、
未重命名表、未重构 ThinkWatch 或配额、未改前端、未新增角色）。

## 1. P1-01 旧库可能被误判为已迁移 → 终态契约（fail closed）

**问题**：原基线校验只看「表/列是否存在」，`gateway_api_key` 只含 `id`/`tenant_id`、
缺 `key_digest` 的残缺旧库也能在 `CREATE ... IF NOT EXISTS` 的静默 no-op 下被放行并
写成「已迁移」，后续运行再也不会补上缺失对象。

**修复**：新增 `internal/migrate/schema_contract.go`，由 migrations 0001–0009 推导出
**终态契约**（10 张表 × 列 × 类型 × 可空性 × 主键 × UNIQUE × 函数式唯一索引
`lower((username)::text)` × 外键 × 命名索引），用 PostgreSQL 目录
（`information_schema.columns` / `pg_constraint` / `pg_indexes`）逐项核对；
任一不符即报错并**点名每一个缺失/漂移对象**，且不写入 `schema_migrations`。
删除旧的弱基线 `internal/migrate/baseline.go`。

**分阶段策略说明**：bootstrap 采用「先应用 → 再校验 → 最后记账」三阶段，而非单事务。
原因是 pgx 简单协议下无法在同一事务内混跑多语句 DDL 脚本与目录查询后再 COMMIT
（实测 `tx is closed`）。由于 `schema_migrations` 只在契约通过后才写入，失败时数据库
零记账行、下次运行会重试，**仍然 fail closed**。

**测试**（`internal/migrate/schema_contract_test.go`）：缺失 `gateway_tenant.code`、
Codex 原始复现的残缺 `gateway_api_key`、10 张表逐一打桩、列类型错、可空性错、
缺失 UNIQUE、完整旧库 bootstrap、删索引、删外键、错误信息可诊断性。

## 2. P1-02 命名不规范的迁移文件被静默忽略 → 严格命名并报错

**问题**：`migrationFilePattern` 过宽，且匹配失败即 `continue`，`0001-Init.SQL`、
`0001 init.sql` 之类会被静默跳过，历史被改写而无人察觉。

**修复**：正则收紧为 `^\d{4}_[a-z0-9][a-z0-9_]*\.sql$`；`.sql` 命名不合法时**报错**
而非跳过（扩展名判断改为大小写不敏感，以覆盖 Windows 大小写不敏感文件系统）；
非 `.sql` 文件继续忽略；按数字版本号排序；延续原有的断号 / 重复版本 / 校验和检测。

## 3. P1-03 认证请求体仅有内存上限，挡不住 Slowloris → 真实 TCP 读超时

**问题**：`io.ReadAll(io.LimitReader(...))` 只限制读到的字节数，判断发生在读完之后；
一个逐字节滴送的连接可以长期占住 goroutine 与连接。

**修复**：`decodeAdminAuthBody(w, r, dst)` 增加 `http.MaxBytesReader`（硬上限）与
`http.NewResponseController(w).SetReadDeadline`（约 5s 读超时）；当
`SetReadDeadline` 返回 `http.ErrNotSupported`（如 `httptest.ResponseRecorder`）时
降级为仅字节上限，不影响单元测试。保留原有的单 JSON 值 / 未知字段严格解码语义。
**未**使用全局 `ReadTimeout`/`WriteTimeout`，以免破坏 SSE 数据面。

**测试**（`internal/app/admin_auth_slowloris_test.go`，真实 TCP）：超上限被拒、
慢速滴送在约 5s 内被切断且 goroutine 释放、正常快速请求不受影响。

## 4. P1-04 知道 sid 即可伪造 Token 触发全家桶撤销 → 签名 v2 Refresh Token

**问题**：refresh 走「既非当前 hash 又非宽限期内前任 → 一律 replay」，攻击者只要
拿到一个真实 sid（claim / 设备列表 / 日志）就能构造 `sid.<任意 secret>`，
让受害者名下**所有会话**被撤销。掌握标识符即可强制他人重新登录。

**修复**：refresh token 改为签名格式 `v2.<sid>.<secret>.<mac>`，
`mac = HMAC-SHA256(admin secret, "refresh-token-mac-v2" || sid || secret)`，
标签与轮换派生标签（`refresh-rotation-v1`）**域隔离**；MAC 比对用 `hmac.Equal`
（恒定时间）。保留 legacy `<sid>.<secret>` 兼容：它只能靠数据库 hash 自证，
首次轮换即透明升级为 v2。裁决点收敛到唯一函数 `replayOrReject`：

- v2 且 MAC 有效 → 证明持有签名密钥；既非当前 hash 又非宽限期内前任 ⇒ **真 replay**，撤销全家桶；
- legacy 或 MAC 无效 → **伪造**，返回 401 `ErrAdminUnauthenticated`，**不撤销任何会话**；
- 撤销失败 → 记录日志并返回 `ErrAdminInternal`，**不谎报 replay**（避免把未发生的事写成安全事件）。

保持：后继 token 确定性派生、5s 宽限（生产默认 `adminRefreshReplayGraceDefault`）、
并发双 tab 收敛、登出同样不被伪造 token 利用。

**测试**（`internal/app/admin_refresh_mac_test.go`）：伪造 legacy / 伪造 v2 MAC 均不撤销、
重复伪造 25×2 次仍不撤销且真 token 仍可用、真 v2 replay 仍撤销全家桶、
legacy 透明升级为带 MAC 的 v2、撤销失败诚实上报、MAC 域隔离、登出不被伪造 token 利用。

## 5. 本轮执行证据

```text
go build ./...                    → 通过
go vet ./...                      → 通过
go test ./... -count=1            → 全绿（app / migrate / proxy / ratelimit / security / storage）
npm --prefix web/admin run check   → 通过

新增：internal/migrate/schema_contract.go、internal/migrate/schema_contract_test.go
      internal/app/admin_auth_slowloris_test.go、internal/app/admin_refresh_mac_test.go
删除：internal/migrate/baseline.go（由终态契约取代）
未纳入提交：.workbuddy-ai/
```

**状态：READY FOR CODEX ROUND 3 REVIEW** —— 未合并、未部署。
复审重点：终态契约的完整性（是否存在 migrations 实际创建但契约未覆盖的对象）、
迁移文件命名正则是否过严、5s 读超时对慢客户端的影响、v2 MAC 的域隔离与兼容路径。
