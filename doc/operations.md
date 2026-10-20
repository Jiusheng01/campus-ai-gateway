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
cd D:\AI\campus-ai-gateway
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

### Docker 卷与同机多个 checkout

网关的全部状态都保存在 Docker 命名卷里，不在仓库中：

- `deploy_postgres_data`：PostgreSQL——管理员、租户、业务 Key、渠道、模型、线路、审计与请求记录
- `deploy_redis_data`：Redis——限流、配额投影与登录限流计数

`deploy/docker-compose.yml` 没有顶层 `name:`，Compose 的项目名取自 compose 文件所在目录，即 `deploy`。因此同一台机器上的**任意 checkout 默认共用 `deploy` 项目与同一组卷**：在第二个 clone 里看到渠道和租户，是前一个 checkout 留下的数据；迁移里没有任何 seed 语句。

需要隔离时，在启动前给该 checkout 指定独立项目名（Compose 会据此派生独立的卷名）：

```powershell
$env:COMPOSE_PROJECT_NAME = "campus-gateway-sandbox"
.\scripts\setup.ps1
```

或直接使用 Compose（网关会拒绝占位符或过短的 `API_KEY_HMAC_SECRET`，compose 文件没有默认值，需先导出一次并保存好）：

```bash
export API_KEY_HMAC_SECRET="$(openssl rand -base64 48)"
docker compose -p campus-gateway-sandbox -f deploy/docker-compose.yml up -d --build
```

### 清空本机数据（reset）

```powershell
.\scripts\reset.ps1          # 需要输入规范化后的 Compose 项目名确认
.\scripts\reset.ps1 -Force   # 跳过输入确认，但仍打印目标项目与卷
```

`reset.ps1` 从 `COMPOSE_PROJECT_NAME` 取项目名（未设置时用默认的 `deploy`），按 Compose 的规则规范化（转小写、只允许 `[a-z0-9_-]`、首字符必须是字母或数字），打印并校验它，然后执行 `docker compose -p <项目名> -f deploy/docker-compose.yml down -v`：删除该项目的容器与命名卷（PostgreSQL 与 Redis 数据全部丢失）。默认项目名对应 `deploy_postgres_data` 与 `deploy_redis_data`。之后调用 `dev.ps1 -ForceRestart` 走常规启动路径：容器重建、迁移重新应用、数据库为空。

确认提示要求输入**上面打印的项目名本身**，而不是固定的 `reset`。这样无法在不看目标的情况下顺手确认；`-Force` 仍然可用，但它会明确打印将要删除的项目与两个卷。

**残留的冲突风险：** `down -v` 只删除匹配到的 Compose 项目的卷，无法判断同一台机器上是否还有另一个 checkout 在用同一个项目名。默认名 `deploy` 下，第二个 checkout 与本 checkout 挂的是同一组卷——在任意一个 checkout 里执行 reset，都会删掉另一个 checkout 的数据。`COMPOSE_PROJECT_NAME` 只影响之后启动的容器与卷，不能把已经写在 `deploy` 名下的数据隔离开；需要隔离时，应在首次启动前就设置独立的项目名。

**这是清空数据的唯一预期方式。** 网关启动时不会删除任何数据，迁移里也没有 seed。只想停止服务而保留数据，用 `.\scripts\stop.ps1`。

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
└── 0013_request_attempt.sql

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
├── reset.ps1
├── verify-failover.ps1
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
- 五角色 RBAC
- 浏览器凭据不落持久化存储

依赖数据库的集成测试需要 PostgreSQL 17 与 Redis 7，CI（`.github/workflows/ci.yml`）以
health-checked service 方式提供，本地则用 `.\scripts\dev.ps1` 起容器。**没有数据库时这些
测试会 SKIP**，因此"CI 绿"必须与"CI 里真的有数据库"一起看。
