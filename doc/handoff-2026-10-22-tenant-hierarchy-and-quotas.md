# 交接说明：租户层级、三级配额与模型池额度（2026-10-22）

本文面向接手本仓库的另一个 agent，说明当前分支相对 `origin/main`（`9d44669`，PR #43 已合并）新增了什么、怎么跑、验证到什么程度、还有哪些坑。

## 1. 运行环境

| 项 | 值 |
| --- | --- |
| 工作副本 | `C:\Users\le\ai-gateway-remote-main`（基于 `origin/main` 9d44669，改动未提交时按本文描述） |
| 服务端口 | `:8080`，后台 UI 为 `/admin-ui`，个人控制台为 `/console` |
| 数据库 | PostgreSQL `campus_gateway_remote`，容器 `deploy-postgres-1`，`127.0.0.1:5432` |
| Redis | 容器 `campus-remote-redis`，`127.0.0.1:16379` |
| 数据面 | `DATA_PLANE=direct`（渠道在进程内直连） |
| 迁移版本 | 19（`schema_migrations`），迁移前备份在 `.local-backups/campus_gateway_remote-pre-0019.sql` |
| 二进制 | `.local-bin/gateway`、`.local-bin/migrate`；日志 `.local-bin/gateway.{out,err}.log` |

启动所需环境变量（**值不入库、不入文档**，使用部署机本地配置）：

- `DATABASE_URL`、`REDIS_ADDR`、`HTTP_ADDR=:8080`、`DATA_PLANE=direct`
- `API_KEY_HMAC_SECRET`（≥16 字节；更换会使已存 API Key 摘要全部失效）
- `ADMIN_AUTH_MODE=session`（UI 登录；`token` 仅作应急，且会禁用 `/admin/users`）
- `ADMIN_SETUP_LOCAL_ONLY=true`、`TRUSTED_PROXY_CIDRS=127.0.0.1/32`
- 生产必须补 `CREDENTIAL_ENCRYPTION_KEY`、`ADMIN_SESSION_SECRET`（当前为派生开发密钥）

不要 source `deploy/.env`，其中的密钥与本地实例不一致。

构建顺序（embed 依赖前端产物）：

```bash
npm --prefix web/admin ci        # 首次
npm --prefix web/admin run build # 必须先于 go build
go build -o .local-bin/gateway ./cmd/server
go build -o .local-bin/migrate ./cmd/migrate
```

> `internal/app/adminui/dist/assets/` 被 gitignore，`dist/index.html` 是受版本控制的占位文件，会随构建更新指向新的 bundle 名。CI 构建前必须先跑前端构建，否则 embed 只会带上占位页。

## 2. 本次交付的能力

### 2.1 租户层级树（用户分配权限层级树）

- `gateway_tenant` 增加 `parent_id` / `path` / `depth` / `kind`；`path` 为物化路径 `/<root>/.../<self>/`，子树判断是前缀比较。
- 接口：`GET/POST /admin/tenants`、`PUT/DELETE /admin/tenants/{id}`、`POST /admin/tenants/{id}/allocation`、`POST /admin/tenants/{id}/move`、`GET /admin/tenants/{id}/usage`、`GET /admin/tenants/{id}/model-quotas`。
- 删除受两条约束：仍有子租户（`tenant_has_children`）、仍是某管理员的管辖范围（`tenant_in_use`）；删除会一并删除该租户下的 API Key，并在响应里返回 `deleted_keys`。
- 移动会重写整棵子树的 `path`，禁止移入自己的子树（`tenant_cycle`）。
- `kind`（school/dept/teacher/student/tenant）只是展示标签，不参与鉴权。

### 2.2 三级配额（留空继承 / 0 不限 / >0 上限）

- 六个执行字段：`daily_token_limit`、`monthly_token_limit`、`monthly_request_limit`、`rpm_limit`、`tpm_limit`、`max_concurrency`。
  - `NULL` = 沿祖先链继承最近一个已设置的值
  - `0` = 本级明确不限
  - `>0` = 本级上限
- 生效值 = 链上所有正数上限的最小值；`monthly_budget_micros` 只存储、不执行，因此没有三态。
- 分配走独立端点 `POST /admin/tenants/{id}/allocation`（全量替换六个字段，缺省键即继承），租户身份编辑走 `PUT`，两者不混用。
- 用量接口返回 `effective`，控制台同时展示「已配置值 / 生效值 / 来源租户」。

### 2.3 模型池额度（模型额度分配）

- 新表 `gateway_model_quota(subject, model_alias, daily_token_limit, monthly_token_limit)`：`tenant_id IS NULL` 表示全校共享池，是模型额度链的根；同一「主体 + 模型」只有一行。
- 接口：`GET/PUT/DELETE /admin/model-quotas`（`DELETE` 用 `?tenant_id=&model_alias=`，空 `tenant_id` 表示全校池）。
- 写入时校验模型别名必须存在于模型目录（`unknown_model`），避免配出永远匹配不上的上限。
- 读取返回 `effective`（`daily_token_limit`/`monthly_token_limit`/`set_by`）与 `own`，即「这一行自己配置的值」和「链上真正生效的值」。
- 数据面与租户链一起在准入时原子扣减（`internal/ratelimit/chain.go` 的单次 Lua 脚本：所有层都放行才写入，拒绝则不写任何计数）。

### 2.4 受限租户管理员（tenant_admin + scope）

- `gateway_admin_user.scope_tenant_id` 限定 `tenant_admin` 只能管理某个子树。
- 读：子树含自身；写：严格下级（不含自身），不能抬高自己的上限。
- 全校模型池只有全局管理员可写；受限管理员只读。
- 会话每次请求从数据库重读 `scope_tenant_id`，修改范围立即生效；`AuthenticationChanged()` 只比较 roles/status，所以改范围不会把用户踢下线。
- 创建账号时 `ValidateAccountScope` 要求 `tenant_admin` 必须带范围，非 `tenant_admin` 不得带范围。

### 2.5 结算与用量采集

- `internal/app/settlement.go`：准入时预扣 token，请求结束后按结果三选一——有 usage 按实际结算、无 usage 保留预估值、未提供服务则退款。
- `internal/app/usage_tracker.go`：流式响应逐行观察 SSE，非流式只保留有界 body（1 MiB），只提取 usage，不缓存整段响应。

## 3. 前端

| 路由 | 页面 | 说明 |
| --- | --- | --- |
| `/admin-ui/tenants` | 租户层级 | 树形表格 + 移动端卡片；折叠、搜索、状态筛选；新增子租户、编辑身份、分配限额、用量抽屉、删除二次确认（输入名称） |
| `/admin-ui/model-quotas` | 模型额度 | 「主体 + 模型」列表，全校池与租户行；新增/编辑/删除（删除即恢复继承） |
| `/admin-ui/users` | 用户与权限 | 角色勾选；勾选 `tenant_admin` 时出现「管理范围」租户选择，未选不能保存；仅改范围时提示「管理范围已更新」，不声称会话失效 |
| `/admin-ui/calls` | API Key | 已移除旧的「租户配额」页签，只保留密钥表；租户配额统一在租户页维护 |
| `/admin-ui/logs` | 调用日志 | 空库时不再白屏：`by_key` 为 `null` 时按空列表处理 |

导航：`tenant_admin` 进入管理端后落到 `/admin-ui/calls`，只能看到其范围内的页面；`EVERYONE` 包含 `tenant_admin`，保证个人资料与安全页可访问。

## 4. 验证状态

- `go test ./...`：全部通过（含 app 35s 的集成用例）。
- 前端：`npx vitest run` 151/151 通过；`npx tsc -b` 无错误；`npm run build` 成功，当前 bundle `index-CP3qQia5.js` / `index-DULANjB0.css`。
- 接口实测（8080，真实库）：登录 → 新建子租户 → 分配 daily=100 → 孙租户 `effective.daily_token_limit=100`；模型池 200 / 子租户 50，`effective=50`、`set_by` 正确；删除后租户数复原。
- 回归用例：`admin_scope_test.go`（受限管理员可/不可达端点）、`RequestLogsPage.test.tsx`（空库 null 集合）、`TenantsPage.test.tsx`（创建携带 code/name、空表单拦截）。
- **未做**：浏览器点击走查。browser-use 的调试通道被占用（`take_snapshot` 报 "Browser target debugger is already in use"），只做了单元/接口级验证。

## 5. 已知问题与限制

1. **没有真实上游凭据**：模型为演示配置，真实调用仍会失败；上线前必须完成「真实凭据 + 稳定别名 + 真实线路 + 端到端验收」。
2. **无全局错误边界**：任意页面渲染异常仍会导致整站白屏（调用日志的崩溃已修，但边界未加）。
3. **管理端账号**：本库只有 `admin`（super_admin）。其密码在 2026-10-22 由使用者重置，值不写入仓库；不要在没有确认的情况下再次重置。
4. **会话/加密密钥为派生开发值**：重启或换机器会使会话失效；生产必须显式设置。
5. **启动时会重建配额计数**：日志显示从请求日志重建了 0 条计数；有历史流量时该步骤可能耗时。
6. **`kind` 不参与鉴权**，不要把它当权限用。
7. **工作区是脏的**：PR 未合并前不要 `git checkout -- .` 或清理未跟踪文件；`.local-bin/` 与 `.local-backups/` 已加入 gitignore，里面是本地二进制、日志和含密码哈希的备份，禁止提交。
8. **旧实现参考**：`C:\Users\le\ai-gateway`（旧分支 `feature/tenant-hierarchy-v2`）仅作语义参考，不要在那里继续开发；两边数据库互不相干。

## 6. 建议的下一步

1. 配置真实上游凭据并通过模型测试，完成首个真实调用（Phase 0）。
2. 在浏览器里走查租户树、模型额度、用户管理范围三条主路径，补截图验收。
3. 在路由层加错误边界，单页异常显示错误卡片而非白屏。
4. 生产化：显式设置会话/加密密钥，启用 HTTPS，补备份与监控。
5. 为受限管理员补一条端到端用例：两个租户各建一个 `tenant_admin`，验证互不可见、不能改自身预算。
