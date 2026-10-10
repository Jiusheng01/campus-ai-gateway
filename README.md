# Campus AI Gateway

校园大模型统一接入、路由、限流与安全审计网关。当前仓库先实现 M0：**Virtual Key → RPM/TPM/并发控制 → SSE 透明代理 → ThinkWatch Core**。

## 架构

```text
Client
  |
  v
Nginx
  |
  v
Go Gateway Edge
  |- Virtual Key
  |- IP / Model allowlist
  |- RPM / TPM / Concurrency
  |- Request-ID
  v
ThinkWatch Core
  |- Routing
  |- Failover
  |- Circuit Breaker
  |- Guard
  v
LLM Providers
```

## 目录

```text
cmd/server            HTTP 服务入口
cmd/keygen            Virtual Key 生成工具
internal/app          服务装配与中间件
internal/security     API Key HMAC
internal/ratelimit    Redis 限流
internal/proxy        ThinkWatch 透明代理
internal/storage      PostgreSQL / Redis
migrations            数据库初始化
deploy                Docker / Nginx
```

## M0 已包含

- HMAC-SHA256 Virtual API Key 校验
- Key 状态与过期时间校验
- IP 白名单
- 模型白名单
- Redis RPM 限流
- Redis TPM 预占
- 并发数限制
- X-Request-Id
- SSE 透明转发，关闭代理缓冲
- PostgreSQL 基础数据模型
- Dockerfile / Docker Compose / Nginx 配置

## 本地启动

1. 准备 PostgreSQL 与 Redis。
2. 执行 `migrations/0001_init.sql`。
3. 复制 `.env.example` 并配置环境变量。
4. 确保 ThinkWatch Core 已启动，并填写 `THINKWATCH_URL` 与 `THINKWATCH_GATEWAY_KEY`。
5. 启动网关：

```bash
go run ./cmd/server
```

## 生成 Virtual Key

```bash
export API_KEY_HMAC_SECRET='replace-with-a-long-random-secret'
go run ./cmd/keygen
```

工具只输出一次完整 Key，同时输出 `key_prefix` 和 `key_digest`。数据库只保存摘要，不保存完整 Key。

示例插入：

```sql
INSERT INTO gateway_tenant(code, name)
VALUES ('itc', '信息网络中心');

INSERT INTO gateway_api_key(
  tenant_id, name, key_prefix, key_digest,
  allowed_models, rpm_limit, tpm_limit, max_concurrency
)
SELECT id, 'demo-key', '<key_prefix>', '<key_digest>',
       '["campus-smart"]'::jsonb, 60, 100000, 10
FROM gateway_tenant
WHERE code='itc';
```

客户端调用：

```bash
curl http://localhost:8088/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [{"role":"user","content":"你好"}],
    "stream": true
  }'
```

## 下一阶段

M1 将补齐：Provider / Model 管理、租户管理、Key 管理 API、ThinkWatch 配置渲染、请求审计、安全事件与 Casbin RBAC。

## ThinkWatch Core

本项目不直接复制 ThinkWatch Core 源码，M0 通过 HTTP 数据面接入。路由、Failover、Circuit Breaker、Guard 等能力继续由 ThinkWatch Core 承担，避免过早深改 Rust 核心。
