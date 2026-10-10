# Campus AI Gateway

校园大模型统一接入、模型路由、限流与安全审计网关。

本项目定位在校园 AI 平台的 **模型网关 / 模型路由 / 访问控制 / 审计** 层：对上提供 OpenAI-compatible API，对下通过 ThinkWatch Core 接入 Qwen 等模型服务，并在 Go Gateway 中承担租户、Virtual API Key、配额、模型权限与请求审计。

> 当前状态：**M0 已完成端到端真实链路验收，M1 控制面与管理后台已可用。**
>
> 已真实验证：Virtual Key → Go Gateway → ThinkWatch Core → 阿里百炼 Qwen → Token Usage → PostgreSQL 审计日志。

---

## 1. 当前能力

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| OpenAI-compatible API | ✅ | /v1/models、/v1/chat/completions |
| Virtual API Key | ✅ | sk-campus-*，HMAC-SHA256 摘要存储 |
| 租户管理 | ✅ | 新增、编辑、启用/停用 |
| Key 状态 / 过期校验 | ✅ | 支持禁用和过期时间 |
| 模型白名单 | ✅ | 每个 Key 独立授权模型 |
| IP 白名单 | ✅ | 每个 Key 独立配置 |
| RPM 限流 | ✅ | Redis |
| TPM 控制 | ✅ | 当前为请求前预占式 |
| 最大并发控制 | ✅ | Redis |
| Request ID | ✅ | X-Request-Id |
| SSE 透明代理 | ✅ | 支持流式返回，不缓冲客户端响应 |
| Token Usage 采集 | ✅ | 非流式 + OpenAI SSE 最终 usage chunk |
| 请求审计 | ✅ | PostgreSQL gateway_request_log |
| ThinkWatch Core 接入 | ✅ | HTTP 数据面接入 |
| Qwen 真实调用 | ✅ | campus-smart 已完成端到端验收 |
| Provider 管理 | ✅ | 新增、编辑、启用/停用 |
| Model 管理 | ✅ | 虚拟模型 → Provider → 上游模型 |
| ThinkWatch 配置同步 | ✅ | 生成前校验、备份、原子替换 |
| 网页模型测试 | ✅ | 后台直接测试模型、Token、延迟 |
| 内置 Web 管理后台 | ✅ | 无需单独 Node/Vue 进程 |
| Windows 一键初始化 | ✅ | scripts/setup.ps1 |
| Windows 一键开发启动 | ✅ | scripts/dev.ps1 |
| 多 Provider Failover 控制面 | 🚧 | 下一阶段 |
| 实际 Provider / 上游模型回填 | 🚧 | 下一阶段 |
| SSO / RBAC | 🚧 | 下一阶段 |
| 30,000 RPM 压测验收 | 🚧 | 尚未完成目标压测 |

---

## 2. 架构

~~~text
业务系统 / AI 应用
        │
        │ OpenAI-compatible API
        ▼
┌────────────────────────────────────────────┐
│            Campus AI Gateway               │
│                                            │
│ Tenant / Virtual Key                       │
│ HMAC Auth / Expire / Status                │
│ Model & IP Allowlist                       │
│ RPM / TPM / Concurrency                    │
│ Request ID / Audit / Usage                 │
│ Admin API / Embedded Admin UI              │
└──────────────────┬─────────────────────────┘
                   │
                   ▼
┌────────────────────────────────────────────┐
│              ThinkWatch Core               │
│ Routing / Failover / Circuit Breaker / Guard│
└──────────────────┬─────────────────────────┘
                   │
                   ▼
           LLM Providers
       Qwen / DeepSeek / ...

Data:
  PostgreSQL -> tenants / keys / providers / models / audit
  Redis      -> RPM / TPM / concurrency
~~~

当前阶段 **Go Gateway 是校园侧控制面和访问控制边界**，ThinkWatch Core 负责底层模型数据面能力。项目不直接复制 ThinkWatch Core 源码，避免过早深改 Rust 核心。

---

## 3. 已完成真实验收

### 3.1 业务链路

已实际验证：

~~~text
sk-campus-* Virtual Key
        ↓
租户 / Key 鉴权
        ↓
模型白名单
        ↓
RPM / TPM / 并发控制
        ↓
Go Gateway
        ↓
ThinkWatch Core
        ↓
campus-smart
        ↓
Qwen
        ↓
OpenAI-compatible Response
        ↓
Token / latency / HTTP status
        ↓
PostgreSQL 审计
~~~

真实请求已返回：

- model = campus-smart
- HTTP 200
- prompt / completion / total tokens
- 请求延迟
- 对应 Request ID
- 请求日志落库

### 3.2 已验证异常路径

- 错误 Virtual Key → 401
- 模型越权 → 403
- RPM 超限 → 429
- 并发超限 → 429
- SSE 流式返回正常
- ThinkWatch Provider 凭据缺失时能返回明确错误

---

## 4. 内置管理后台

启动 Gateway 后访问：

~~~text
http://127.0.0.1:8080/admin-ui
~~~

当前页面：

~~~text
校园模型网关
├── 总览
│   ├── Gateway
│   ├── PostgreSQL
│   ├── Redis
│   ├── ThinkWatch
│   ├── 今日请求
│   ├── 今日 Tokens
│   ├── 今日错误
│   └── 平均延迟
├── Provider 管理
├── 模型管理
│   ├── 同步到 ThinkWatch
│   └── 测试模型
├── 租户管理
├── API Key 管理
└── 请求日志
~~~

管理 API 与业务 Virtual Key 分离：

- 管理端：ADMIN_TOKEN
- 业务端：sk-campus-*

本地开发后台会把 ADMIN_TOKEN 保存到浏览器 localStorage，方便开发调试。

> 该方式仅适用于本地开发。生产环境必须改为正式登录会话、统一身份认证与 RBAC。

---

## 5. Windows 本地开发

### 5.1 第一次：一键初始化

~~~powershell
cd D:\AI\campus-ai-gateway
.\scripts\setup.ps1
~~~

脚本会：

- 检查 Go / Docker
- 自动寻找 twcore.exe
- 保存 TWCORE_EXE
- 设置 THINKWATCH_CONFIG_PATH
- 校验 ThinkWatch 配置
- 从 ThinkWatch clients 配置识别 tw-* Gateway Key
- 保存 THINKWATCH_GATEWAY_KEY
- 检查 DASHSCOPE_API_KEY
- 缺少 Provider Key 时使用隐藏输入
- 自动启动开发环境

如果 ThinkWatch Core 不在常见目录：

~~~powershell
.\scripts\setup.ps1 -TwCorePath "D:\AI\ThinkWatch-Core\twcore.exe"
~~~

只初始化、不启动：

~~~powershell
.\scripts\setup.ps1 -NoStart
~~~

### 5.2 日常开发

以后通常只需要：

~~~powershell
.\scripts\dev.ps1
~~~

如果 8080 上有旧 Gateway：

~~~powershell
.\scripts\dev.ps1 -ForceRestart
~~~

查看状态：

~~~powershell
.\scripts\status.ps1
~~~

停止 Gateway + PostgreSQL + Redis：

~~~powershell
.\scripts\stop.ps1
~~~

---

## 6. Provider 与模型管理

Provider 当前保存：

- 名称
- Base URL
- 协议
- Provider 凭据环境变量名
- 启用状态

例如 Qwen：

~~~text
name:           qwen
protocol:       openai-chat
credential_env: DASHSCOPE_API_KEY
status:         enabled
~~~

**真实 Provider API Key 不保存到 PostgreSQL，也不通过管理页面持久化。**

当前数据模型：

~~~text
virtual model alias
    ↓
provider
    ↓
upstream model
~~~

已验证示例：

~~~text
campus-smart
    ↓
qwen
    ↓
qwen3.7-flash-2026-07-15
~~~

管理后台“模型管理”提供 **同步到 ThinkWatch**。

同步过程：

~~~text
PostgreSQL Provider / Model
        ↓
生成临时 config
        ↓
twcore check
        ↓
校验成功
        ↓
备份 config.yaml.campus.bak
        ↓
原子替换 config.yaml
        ↓
ThinkWatch reload
~~~

只更新 ThinkWatch 顶层 providers / aliases，保留 listen / clients 等其它配置。

Windows 默认配置路径：

~~~text
%APPDATA%\ThinkWatch\config.yaml
~~~

---

## 7. 租户与 Virtual API Key

关系：

~~~text
Tenant
├── Key A
├── Key B
└── Key C
~~~

同一部门可以按业务系统拆分多个 Key，各自配置：

- 模型白名单
- IP 白名单
- RPM
- TPM
- 最大并发
- 有效期
- 启用 / 停用

完整 Key 形如：

~~~text
sk-campus-...
~~~

完整 Key **只在创建时显示一次**。数据库仅保存 key_prefix 和 HMAC-SHA256 key_digest，不保存完整明文 Key。

---

## 8. API 调用

### Models

~~~bash
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-campus-..."
~~~

### Chat Completions

~~~bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-campus-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "campus-smart",
    "messages": [
      {"role": "user", "content": "你好"}
    ],
    "stream": false
  }'
~~~

---

## 9. 请求审计

认证通过的 /v1/* 请求写入 gateway_request_log。

当前记录：

- request_id
- tenant_id
- api_key_id
- client_ip
- requested_model
- actual_model
- provider
- stream
- input tokens
- output tokens
- total tokens
- latency_ms
- status_code
- error_code
- created_at

其中 provider 字段已经预留，但当前还没有从 ThinkWatch telemetry 中可靠回填真实 Provider。

### 当前已知限制

- TPM 当前是**预占式**，不是最终 usage 的严格实时结算。
- 请求日志中的真实 Provider / 最终上游模型还需结合 ThinkWatch telemetry / trace 回填。
- 管理后台请求日志目前是基础列表，还未完成高级筛选、分页、导出。

---

## 10. 目录

~~~text
cmd/
├── server/              Gateway HTTP 服务入口
└── keygen/              Virtual Key 工具

internal/
├── app/                 Server / middleware / Admin API / embedded UI
│   └── adminui/         内置 Web 管理后台
├── httpx/               HTTP 公共能力
├── model/               数据结构
├── proxy/               ThinkWatch 透明代理
├── ratelimit/           Redis 限流
├── security/            Virtual Key HMAC
└── storage/             PostgreSQL / Redis

migrations/
├── 0001_init.sql
└── 0002_management.sql

deploy/
├── docker-compose.yml
├── docker-compose.dev.yml
└── nginx/

scripts/
├── setup.ps1
├── dev.ps1
├── status.ps1
└── stop.ps1
~~~

---

## 11. 与校园 AI 项目需求的对应关系

本仓库不是完整“校园 AI 助手门户 / 智能体 / 知识库”产品，而是其中的 **统一模型接入和安全治理基础设施**。

| 校园 AI 需求方向 | 本项目当前状态 |
| --- | --- |
| 主流大模型统一接入 | Qwen 已真实接通；其它 Provider 待接入 |
| 模型管理 | Provider / Model 后台已完成 |
| 模型自由切换基础 | 虚拟模型 Alias 已完成；多 Provider 绑定待完成 |
| 调用权限管理 | Tenant + Virtual Key + Model/IP Allowlist 已完成 |
| 高并发限流 | RPM / TPM / Concurrency 已实现；目标压测未完成 |
| 调用日志 | Request ID / Token / latency / status 已完成 |
| 本地化部署基础 | Go + PostgreSQL + Redis + ThinkWatch 本地运行 |
| 安全审计 | Key 摘要存储 + 请求审计已完成 |
| 角色权限 | 尚未完成 SSO / RBAC |
| 多模型 Failover | ThinkWatch 具备底层能力；本项目控制面配置尚在开发 |
| 30,000 RPM | **尚未完成目标压测，不能视为已满足** |
| HTTPS / 正式安全部署 | Nginx 基础存在，生产硬化尚未完成 |

---

## 12. Roadmap

### M1：模型控制面

- [x] Provider 管理
- [x] Model 管理
- [x] Tenant 管理
- [x] Virtual Key 管理
- [x] ThinkWatch 配置同步
- [x] 网页模型测试
- [ ] 一个虚拟模型绑定多个 Provider
- [ ] Provider 优先级
- [ ] Failover 路由
- [ ] Circuit Breaker 状态展示
- [ ] 实际 Provider / 上游模型审计回填

### M2：安全与运营

- [ ] 学校统一身份认证
- [ ] RBAC
- [ ] 管理员操作日志
- [ ] 安全事件
- [ ] Guard / 敏感策略事件展示
- [ ] 审计筛选 / 分页 / 导出
- [ ] 租户维度统计

### M3：性能与生产化

- [ ] TPM 精确结算
- [ ] 分布式部署验证
- [ ] 30,000 RPM 目标压测
- [ ] 突发流量压测
- [ ] SSE 高并发压测
- [ ] HTTPS
- [ ] Secret Manager / KMS
- [ ] PostgreSQL / Redis 高可用
- [ ] 监控、告警与容量规划

---

## 13. 安全说明

本地开发默认值仅用于开发。

正式部署前必须替换：

- ADMIN_TOKEN
- API_KEY_HMAC_SECRET
- PostgreSQL 密码
- Redis 安全配置
- Provider credentials

生产环境不得继续使用浏览器 localStorage 作为管理员长期认证方案，应接入：

~~~text
学校统一身份认证
        ↓
后台登录 Session
        ↓
RBAC
        ↓
管理操作审计
~~~

Provider API Key 推荐通过环境变量、Secret Manager、KMS / Vault 管理，不应写入代码仓库或业务数据库。

---

## 14. 当前结论

项目已经从概念验证进入：

> **可运行、可管理、可真实调用、可限流、可审计的校园大模型统一接入网关。**

当前最重要的下一步：

> **多 Provider 绑定 → 优先级 → Failover → 实际路由审计 → 30,000 RPM 目标压测。**
