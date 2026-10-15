# 生产部署与高可用

## 1. 边缘入口

HTTPS 应在托管负载均衡器或 Nginx 上终止。

要求：

- SSE 禁止代理缓冲
- 上游读取超时应足够长
- PostgreSQL、Redis、ThinkWatch 控制端口和 Gateway 管理端口不要直接暴露到公网

## 2. Gateway

生产建议至少运行两个无状态 Gateway 实例，并置于负载均衡器之后。

共享状态放在：

- PostgreSQL
- Redis

健康检查：

- `/healthz`：进程存活
- `/readyz`：PostgreSQL + Redis 就绪
- `/metrics`：Prometheus 文本格式业务指标

程序已支持 SIGINT/SIGTERM 优雅退出，并提供 10 秒 Shutdown 窗口。

对于长时间 SSE 连接，容器编排平台的 termination grace period 可能需要设置得更长。

## 3. Secret

生产环境的敏感配置必须来自 Secret 管理机制，而不是提交到仓库：

- `API_KEY_HMAC_SECRET`
- `ADMIN_SESSION_SECRET`
- `ADMIN_PROXY_SECRET`
- PostgreSQL / Redis 凭据
- `THINKWATCH_GATEWAY_KEY`
- `DASHSCOPE_API_KEY`
- `DEEPSEEK_API_KEY`
- `ARK_API_KEY`
- 校园 OIDC Client Secret

推荐：

- Kubernetes Secret
- Vault
- KMS / Secret Manager

### ADMIN_SESSION_SECRET

管理员会话专用密钥，用于 Access Token 的 HS256 签名与 Refresh Token 轮换派生。
它与 `API_KEY_HMAC_SECRET` 属于**不同安全域**，必须单独设置，不能复用。

```text
openssl rand -base64 48
```

要求至少 32 字节。未配置时网关会从 `API_KEY_HMAC_SECRET` 经带标签的 HMAC 派生一个
开发用密钥，并在启动日志打印警告；该回退只为本地开发开箱即用，生产必须显式配置。

### TRUSTED_PROXY_CIDRS 与首次初始化

`TRUSTED_PROXY_CIDRS` 列出可信反向代理地址。`X-Forwarded-For` / `Forwarded` /
`X-Real-IP` 只有在直连 peer 属于该列表时才会被采信。

首次初始化（`ADMIN_SETUP_LOCAL_ONLY=true`，默认）判断的是**解析后的真实客户端地址**，
而不是最后一跳 peer：

- 本机直连、且不带转发头 → 允许
- 可信代理转发、解析出的客户端为本机 → 允许
- 可信代理转发、解析出的客户端为公网/内网其它地址 → 拒绝
- 本机 peer 但带转发头、而该 peer 不在 `TRUSTED_PROXY_CIDRS` 中 → 拒绝（fail closed）

因此，**在反向代理后面部署时，远程访问者无法通过代理创建第一个管理员账号**。
运维必须先在本机完成初始化，或使用 `ADMIN_BOOTSTRAP_USERNAME` /
`ADMIN_BOOTSTRAP_PASSWORD` 进行无人值守初始化。


## 4. PostgreSQL

推荐生产基线：

- 托管 PostgreSQL，或 Primary + Standby
- 自动备份
- Point-in-time Recovery
- 连接池
- TLS
- 已验证的恢复流程
- 监控连接数、连接耗尽和慢查询

### 数据库迁移

Schema 由独立的 `cmd/migrate` 命令（实现位于 `internal/migrate`）管理，它是 schema
的唯一来源：

```powershell
go run ./cmd/migrate -database-url "$env:DATABASE_URL" -migrations .\migrations
```

- 按文件名顺序应用 `migrations/` 下全部版本，每个文件在自己的事务内执行，
  `schema_migrations`（version / filename / checksum / applied_at）与业务 DDL
  同一事务提交：失败的文件不会留下记录，重跑只会重试它。
- 已记录文件的校验值变化会直接失败。历史迁移不允许就地改写，改动历史等于要
  拿线上数据赌一个猜测。
- 没有 `schema_migrations` 的旧库（包括只应用过 `0001` 的部署）会按顺序重放全部
  幂等迁移后再建立记录，一条命令补齐到最新版本。
- 并发执行由 PostgreSQL advisory lock 串行化，两个实例同时启动也不会交叉应用。
- 数据库不可达、权限不足、SQL 失败或校验冲突时命令非零退出。

Docker Compose 栈把迁移做成一次性服务门禁：`migrate` 服务先运行，
`gateway-server` 声明 `depends_on: migrate: condition: service_completed_successfully`，
迁移失败时网关不会启动。镜像内只保留一份迁移源文件（`/migrations`），
`docker-entrypoint-initdb.d` 的双轨装载已移除，避免两套机制产生不同结构。

## 5. Redis

推荐生产基线：

- 托管 HA Redis / Sentinel / Cluster
- 认证与 TLS
- 不对公网开放
- 根据限流/配额语义选择持久化策略
- 监控内存、Eviction、延迟和重连

## 6. ThinkWatch

ThinkWatch Core 继续作为运行时：

- Routing
- Failover
- Circuit Breaker

控制接口应放在管理网络。

Campus Gateway 每次写入 ThinkWatch 配置前仍必须通过 `twcore check`，校验成功后再替换正式配置。

## 7. 监控

Prometheus 可抓取 `/metrics`。

示例配置：

```text
deploy/monitoring/prometheus.yml
```

至少应对以下情况告警：

- Readiness 失败
- HTTP 错误率升高
- P95 / P99 延迟升高
- Failover 次数异常增加
- Redis / PostgreSQL 不可用
- ThinkWatch Provider Health / Circuit Open
- 磁盘或备份失败

## 8. HA 当前状态

仓库已经提供：

- 无状态 Gateway 设计
- Liveness / Readiness
- Prometheus 指标
- 双 Gateway HTTPS Nginx 示例
- PostgreSQL / Redis / LB 高可用建议

但真正的 PostgreSQL / Redis / Load Balancer 故障切换仍属于基础设施验收项。

在目标环境实际测试之前，不应宣称 HA 已通过生产验证。
