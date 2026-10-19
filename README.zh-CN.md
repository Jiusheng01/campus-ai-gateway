# Campus AI Gateway

[English](README.md) · **简体中文**

[![ci](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/Jiusheng01/campus-ai-gateway/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

一个校园大模型网关：对多个模型供应商提供**统一的 OpenAI 兼容入口**，并承担租户、业务 API Key、配额、限流、路由与故障切换、熔断、审计与管理控制台。单个 Go 进程 + PostgreSQL + Redis，没有 sidecar，也没有第二个数据面。

> 它不是聊天机器人、智能体框架或知识库。它是这些东西**下面那一层**：**统一模型接入 + 访问控制 + 路由治理 + 配额 + 审计 + 管理控制面。**

---

## 为什么需要它

- **一个 API，多个供应商。** 调用方始终只说 `/v1/chat/completions`。网关把虚拟模型解析成 Provider，注入该渠道的凭据，并把响应原样流式回传。
- **以 Tenant 为边界。** 配额、限流（RPM / TPM / 并发）与审计都定义在 Tenant 上，所以一个院系是一份预算，而不是每把 key 一份。
- **渠道出问题也扛得住。** 按 `Route Priority` 分带、带内做 smooth weighted round-robin、per-provider 熔断跳过故障渠道，并为每个请求留下完整的 attempt chain。
- **为审计而设计。** 业务请求日志、管理审计与安全事件是三张彼此分离的记录，耐久级别不同；任何上游响应内容都不会落库（ADR-0004、ADR-0009）。

---

## 架构

```text
业务系统 / AI 应用
        │
        │  OpenAI-compatible API
        │  Authorization: Bearer sk-campus-...
        ▼
┌──────────────────────────────────────────────┐
│              Campus AI Gateway               │
│                                              │
│ Tenant / API Key / 模型与 IP 白名单           │
│ RPM / TPM / 并发 / 配额                       │
│ Request ID / 用量 / 审计                      │
│                                              │
│ 管理员会话认证 / RBAC / 管理审计               │
│ 内嵌管理台                                    │
│                                              │
│ 直连数据面（进程内）                           │
│ Routing · Failover · Circuit breaker         │
└────────────────────┬─────────────────────────┘
                     │
          ┌──────────┴──────────┐
          ▼                     ▼
   Alibaba Cloud Qwen     Volcengine Ark
        / ...                  / ...

数据：
  PostgreSQL -> 租户 / 密钥 / 渠道 / 模型 / 会话 / 审计
  Redis      -> RPM / TPM / 并发 / 登录限流
```

数据面**只有一个**，在网关进程内实现：按 `gateway_model_route` 解析模型、注入渠道凭据、流式回传，并按优先级故障切换。因此只需要 PostgreSQL 与 Redis，别无他物。

---

## 能力

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| OpenAI-compatible API | ✅ | `/v1/models`、`/v1/chat/completions`；保留 Embedding / ASR 代理基础 |
| 业务 API Key | ✅ | `sk-campus-*`；只保存前缀与 HMAC-SHA256 摘要 |
| Tenant 管理 | ✅ | 新增、编辑、启用 / 停用、配额与用量 |
| 模型 / IP 白名单 | ✅ | 每个业务 Key 独立 |
| RPM / TPM / 最大并发 | ✅ | Redis；Token 预占后按最终 usage reconcile |
| 直连数据面 | ✅ | 唯一数据面：按优先级故障切换、加权路由、熔断 |
| 加权路由 | ✅ | 同一 `Route Priority` 带内做 smooth weighted round robin |
| 熔断 | ✅ | per-provider，closed / open / half-open，每实例独立 |
| 逐次 attempt 遥测 | ✅ | 每次 Routing Attempt 落 `gateway_request_attempt`，请求详情展示 attempt chain |
| Provider / 虚拟模型 / Route 控制面 | ✅ | 一个虚拟模型可绑定多条 Provider 路线 |
| Request ID / 请求审计 | ✅ | PostgreSQL `gateway_request_log` |
| SSE 透明代理 | ✅ | 流式响应不被缓冲 |
| Admin UI | ✅ | React + TypeScript + Vite，构建产物嵌入 Go 二进制 |
| 管理员会话认证 v2 | ✅ | 首次 Setup、账号密码登录、Access + Refresh、设备会话 |
| 统一账户与开放注册 | ✅ | 公开注册只创建 `member`；管理角色仅由 super_admin 授予 |
| RBAC | ✅ | `super_admin / gateway_admin / tenant_admin / auditor / operator` |
| trusted-header / 校园 SSO 边界 | ✅* | 网关侧完成；学校真实 IdP 待验收 |
| 管理审计 / 安全事件 | ✅ | 与业务请求日志分离 |
| Prometheus / 健康检查 | ✅ | `/healthz`、`/readyz`、`/metrics` |
| 30,000 RPM | 🧊 冻结 | k6 工具已就绪；性能目标等代表性环境实测 |

---

## 快速开始

依赖：**Go 1.24+**、**Docker Desktop**、**Node.js 22+**（仅用于构建管理台）。

Windows（PowerShell）：

```powershell
git clone https://github.com/Jiusheng01/campus-ai-gateway.git
cd campus-ai-gateway
.\scripts\setup.ps1          # 首次：拉起 PostgreSQL / Redis 并初始化
.\scripts\dev.ps1            # 日常启动
.\scripts\status.ps1         # 查看状态
.\scripts\stop.ps1           # 停止
```

Docker Compose：

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

然后打开管理台 <http://127.0.0.1:8080/admin-ui>，在页面里创建第一个管理员。**项目不存在内置账号或默认密码。**

> 网关的全部配置都来自**进程环境变量**，**不读取 `.env` 文件**。[`.env.example`](.env.example) 只是变量名参考清单。详见[配置与凭据](doc/configuration.md)。

---

## 调用 API

在管理台创建租户与业务 Key，并设置该 Key 的模型白名单，然后：

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": false
  }'
```

一个请求会依次经过 Tenant、Key、白名单、配额、限流、路由与审计，最后才到达某个供应商。

---

## 文档

- **[配置与凭据](doc/configuration.md)** —— 环境变量、渠道模板、凭据约束、生产密钥。
- **[认证与账号](doc/authentication.md)** —— 首次 Setup、会话、注册、RBAC、trusted-header / 校园 SSO。
- **[运维](doc/operations.md)** —— 本地开发、数据库迁移、管理台构建、测试与验收。
- **[设计、限制与路线](doc/design.md)** —— 数据面、记录模型、已知限制与下一步。

---

## 状态

网关已经可运行、可管理；Qwen 已完成真实端到端调用，Volcengine Ark 的真实连通已验证。仍然开放、且当前**有意冻结**的部分：校园真实 IdP 验收、Multi-Gateway / PostgreSQL / Redis HA、Embedding / ASR 真实 Provider、以及 30,000 RPM 性能目标。详见[限制与路线](doc/design.md)。

---

## 许可证

MIT 许可证，见 [LICENSE](LICENSE)。
