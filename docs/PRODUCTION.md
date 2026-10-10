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
- `ADMIN_PROXY_SECRET`
- PostgreSQL / Redis 凭据
- `THINKWATCH_GATEWAY_KEY`
- `DASHSCOPE_API_KEY`
- `DEEPSEEK_API_KEY`
- 校园 OIDC Client Secret

推荐：

- Kubernetes Secret
- Vault
- KMS / Secret Manager

## 4. PostgreSQL

推荐生产基线：

- 托管 PostgreSQL，或 Primary + Standby
- 自动备份
- Point-in-time Recovery
- 连接池
- TLS
- 已验证的恢复流程
- 监控连接数、连接耗尽和慢查询

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
