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

---

## 数据库迁移

Schema 由独立命令管理，`dev.ps1` / `setup.ps1` 与 Docker Compose 栈（一次性 `migrate`
服务门禁）都调用同一实现：

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
