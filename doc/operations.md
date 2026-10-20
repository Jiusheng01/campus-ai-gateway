[← 返回 README](../README.md) · 本地开发与验收

本页是运维与开发的实操页面：Windows 本地开发的环境与脚本、数据库迁移、Admin UI 构建、仓库目录结构，以及测试与验收清单（含 E2E 脚本覆盖范围）。

---

## 本地开发环境

- Go 1.24+
- Docker Desktop
- Node.js 22+（仅 Admin UI 构建阶段）

配置注入方式见 [配置与凭据](configuration.md) 的「配置来源」一节：全部通过进程环境变量，项目不加载 `.env`。

### 第一次初始化

```powershell
cd <仓库根目录>
.\scripts\setup.ps1
```

只初始化、不启动：

```powershell
.\scripts\setup.ps1 -NoStart
```

### 日常启动

```powershell
.\scripts\dev.ps1
```

8080 已有旧 Gateway 时：

```powershell
.\scripts\dev.ps1 -ForceRestart
```

只启动、不自动打开浏览器：

```powershell
.\scripts\dev.ps1 -ForceRestart -NoBrowser
```

`dev.ps1` 固定使用直连数据面（`DATA_PLANE=direct`），只需要 PostgreSQL 与 Redis。

查看 / 停止：

```powershell
.\scripts\status.ps1
.\scripts\stop.ps1
```

---

## 数据库迁移

Schema 由独立命令管理，`dev.ps1` / `setup.ps1` 与 Docker Compose 栈里那个**启动前先跑一次的 migrate 服务**都调用同一实现：

```powershell
go run ./cmd/migrate -database-url "postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" -migrations .\migrations
```

- 按顺序应用 `migrations/` 下全部版本，并在 `schema_migrations` 中记录版本、文件名、
  校验值和应用时间；每个文件与自己的记录同一事务提交。
- 已记录文件的校验值变化会失败；没有 `schema_migrations` 的旧库会重放全部幂等迁移后
  建立记录；并发执行由 advisory lock 串行化。
- 迁移失败时命令非零退出，Compose 栈中的网关不会启动。
- **不要用 `psql` 按文件名重放 `migrations/*.sql` 来代替这个命令。** 有些文件是数据迁移：
  `0011_allowed_models_explicit.sql` 会把空的白名单 `[]` / `null` 改写成 `["*"]`。重放它
  会把后来有意配置的「拒绝全部模型」改成「允许全部」，而且每次启动都会扩大一次权限。
  `migrate` 通过 `schema_migrations` 避开这个问题。

---

## Admin UI 构建

Admin UI 源码位于：

```text
web/admin/
```

检查与构建：

```powershell
npm --prefix .\web\admin install
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
```

生产运行时只使用构建后嵌入 Go 的静态资源，不需要单独运行 Node 服务。

Admin UI 通过 `go:embed` 打进二进制，所以构建顺序必须是 **先 `npm run build`，再 `go build`**。替换二进制后还必须重启网关进程，否则端口上运行的仍是旧代码和旧 UI。

---

## 目录结构

```text
cmd/
├── server/                 Gateway HTTP 服务入口
├── keygen/                 业务 API Key 工具
└── migrate/                数据库迁移命令入口

internal/
├── app/                    Server / Admin API / Auth / Embedded UI
│   ├── direct_proxy.go     直连数据面（唯一数据面）
│   ├── breaker.go          每渠道熔断器（内存中）
│   ├── attempts.go         逐次 attempt 遥测收集
│   └── adminui/dist/       Admin UI v3 构建产物
├── httpx/                  HTTP 公共能力
├── migrate/                迁移执行器（schema_migrations / 校验值 / advisory lock）
├── model/                  数据结构
├── ratelimit/              Redis 限流
├── security/               API Key HMAC / Password / JWT
└── storage/                PostgreSQL / Redis

migrations/
├── 0001_init.sql
├── 0002_management.sql
├── 0003_model_routes.sql
├── 0004_route_audit.sql
├── 0005_security_events.sql
├── 0006_quota.sql
├── 0007_admin_users.sql
├── 0008_admin_sessions.sql
├── 0009_admin_session_refresh_rotation.sql
├── 0010_analytics_indexes.sql
├── 0011_allowed_models_explicit.sql
├── 0012_rate_limits_on_tenant.sql
├── 0013_request_attempt.sql
├── 0014_tenant_hierarchy.sql
├── 0015_admin_scope.sql
└── 0016_model_quota.sql

web/
└── admin/                  React + TypeScript + Vite

deploy/
├── Dockerfile
├── docker-compose.yml
├── docker-compose.dev.yml
├── nginx.conf
├── nginx.production.example.conf
└── monitoring/             Prometheus 示例配置

loadtest/                   k6 压测脚本（k6-chat.js / k6-models.js）

scripts/
├── setup.ps1
├── dev.ps1
├── status.ps1
├── stop.ps1
├── verify-failover.ps1
├── verify-three-state.sh
└── e2e-admin-auth-v2.sh
```

---

## 测试与验收

常规检查：

```powershell
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
go test -count=1 ./...
go vet ./...
go build ./...
```

管理员认证 E2E：

```bash
bash scripts/e2e-admin-auth-v2.sh
```

认证 v2 已覆盖：

- 首次本机 Setup
- 反向代理远程绕过防护
- 用户名 / 密码登录
- Argon2id / bcrypt 迁移
- Access / Refresh Session
- 多标签页并发 Refresh
- Replay grace / replay detection
- Session revoke
- 改密全会话失效
- CSRF
- 登录限流
- trusted-header
- 六角色 RBAC
- 浏览器凭据不落持久化存储

租户树、三态额度、按模型额度与受限管理员验收（需要 8080 上已运行的网关）：

```bash
bash scripts/verify-three-state.sh
```

覆盖：

- 租户树的父子关系、深度与物化路径
- 三态分配（`NULL` / `0` / `>0`）与有效额度解析
- 祖先上限不可被下级放大：下级声明更大的值，有效值仍是祖先的
- 按模型额度的全校共享池与租户行
- 受限 `tenant_admin`：不能改自己租户的预算或模型额度、不能写共享池、可以写严格下级
- 验证失败的退出码等于失败检查数

脚本只为专属别名 `e2e-three-state-probe` 写入额度，并在正常结束和异常退出时都删除自己创建的行（包括全校共享池）。它需要本地 `deploy-postgres-1` 容器来播种受限管理员账号，并使用本地开发凭据。

数据面整条链路验收（渠道 → 模型 → 线路 → 密钥 → `/v1` 请求 → 限额拒绝）：

```bash
bash scripts/verify-data-plane.sh
```

覆盖：

- 渠道建的上游模型列表真的带凭据取到了上游的模型名
- 真实 `/v1/chat/completions` 请求穿透网关返回上游结果，并带 usage
- 请求被记进业务请求日志
- 按模型额度真的开始拒流，错误码是 `model_token_quota_exceeded`
- 结束后不留下渠道、模型、线路、密钥

这个脚本自带一个假上游（`scripts/fake-upstream.py`）并**另起一个私有网关实例**（默认 18090），
因为渠道凭据是数据面在请求时从环境变量读的，8080 上那个实例没有这里用的凭据。
**它不会重启或改动 8080 上的服务**；假上游响应固定的 usage，好让额度断言有确定的算术。
端口可用 `GATEWAY_PORT` / `UPSTREAM_PORT` 覆盖。

依赖数据库的集成测试需要 PostgreSQL 17 与 Redis 7，CI（`.github/workflows/ci.yml`）以
health-checked service 方式提供，本地则用 `.\scripts\dev.ps1` 起容器。**没有数据库时这些
测试会 SKIP**，因此"CI 绿"必须与"CI 里真的有数据库"一起看。
