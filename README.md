# Campus AI Gateway

校园大模型统一接入、路由、限流与安全审计网关。当前 M0 已完成真实链路验收，并开始进入 M1：**Virtual Key → RPM/TPM/并发控制 → SSE 透明代理 → ThinkWatch Core → 请求审计**。

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
  |- Request audit
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
internal/app          服务装配、中间件与审计采集
internal/security     API Key HMAC
internal/ratelimit    Redis 限流
internal/proxy        ThinkWatch 透明代理
internal/storage      PostgreSQL / Redis
migrations            数据库初始化
deploy                Docker / Nginx
```

## M0 已验证

- HMAC-SHA256 Virtual API Key 校验
- Key 状态与过期时间校验
- IP 白名单
- 模型白名单
- Redis RPM 限流
- Redis TPM 预占
- 并发数限制
- X-Request-Id
- SSE 透明转发，关闭代理缓冲
- ThinkWatch Core 路由
- `campus-smart` 模型别名
- 阿里百炼 Qwen 真实调用
- OpenAI-compatible usage 返回
- PostgreSQL 基础数据模型

真实验收已覆盖：错误 Key 返回 401、模型越权返回 403、RPM 超限返回 429、并发超限返回 429、SSE 流式返回正常。

## M1 当前已加入

认证通过的 `/v1/*` 请求会写入 `gateway_request_log`，当前记录：

- `request_id`
- `tenant_id`
- `api_key_id`
- `client_ip`
- `requested_model`
- 响应中的 `model`
- `stream`
- input / output / total tokens
- `latency_ms`
- `status_code`
- `error_code`

流式 OpenAI SSE 会从最终 usage chunk 中提取 token 用量。审计采集只复制响应数据用于解析，不缓冲客户端响应。

当前还没有从 ThinkWatch telemetry 中回填实际 provider / 最终上游模型；这部分放在后续 M1。

## 本地启动

本机直接运行 Go Gateway 时，使用开发 override 只把 PostgreSQL / Redis 暴露到 `127.0.0.1`：

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d postgres redis
```

执行 `migrations/0001_init.sql` 后，配置环境变量并确保 ThinkWatch Core 已启动：

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
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [{"role":"user","content":"你好"}],
    "stream": true
  }'
```

## 查看审计日志

完成一次请求后：

```sql
SELECT
  request_id,
  requested_model,
  actual_model,
  stream,
  input_tokens,
  output_tokens,
  total_tokens,
  latency_ms,
  status_code,
  error_code,
  created_at
FROM gateway_request_log
ORDER BY id DESC
LIMIT 20;
```

Docker 本地环境可直接执行：

```bash
docker compose -f deploy/docker-compose.yml exec -T postgres \
  psql -U campus -d campus_gateway \
  -c "SELECT request_id, requested_model, actual_model, stream, input_tokens, output_tokens, total_tokens, latency_ms, status_code, error_code, created_at FROM gateway_request_log ORDER BY id DESC LIMIT 20;"
```

## ThinkWatch Core

本项目不直接复制 ThinkWatch Core 源码，通过 HTTP 数据面接入。路由、Failover、Circuit Breaker、Guard 等能力继续由 ThinkWatch Core 承担，避免过早深改 Rust 核心。

## 下一阶段

M1 后续继续补齐：

- Provider / Model 管理
- Tenant / Virtual Key 管理 API
- ThinkWatch 配置渲染与热更新
- 实际 Provider / 上游模型回填
- 安全事件
- Casbin RBAC
- Vue 管理后台

正式部署前还需要把本地开发密码、HMAC secret 和 Provider credentials 全部改成外部安全配置。
