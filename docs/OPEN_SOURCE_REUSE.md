# 开源复用规范

> 状态：生效中  
> 适用范围：所有新的 Gateway 开发  
> 原则：**能复用成熟开源能力，就不要重复实现基础设施。**

## 1. 开发前必须做复用检查

开始任何非小型功能前，任务或 PR 中先回答：

1. ThinkWatch 是否已经提供该能力？
2. New API 是否已有成熟实现或数据模型？
3. Maskit 是否已有可复用的安全/代理实现？
4. Go 生态是否已有成熟库？
5. 上游项目 License 是什么？
6. 能否直接依赖或调用，而不是复制源码？
7. 如果必须复制，最小复制范围是什么？需要保留哪些声明？
8. 如果仍然自研，原因是什么？

如果已有稳定且兼容的成熟实现，新增自研基础设施代码必须说明理由。

## 2. 复用优先级

按以下顺序选择：

1. **直接调用已有服务 / API / 进程**
2. **增加成熟上游库依赖**
3. **围绕上游实现 Adapter**
4. **在 License 允许的前提下复制最小模块并保留声明**
5. **参考行为/设计重写**
6. **完全从零实现**

优先松耦合集成，避免深度 Fork。

## 3. 当前上游决策

| 项目 | 已观察到的 License | 在 Campus AI Gateway 中的角色 | 当前决策 |
| --- | --- | --- | --- |
| ThinkWatch Core | MIT | Routing / Failover / Circuit Breaker / Guard / 数据面 | **优先直接复用**；继续作为路由执行引擎，通过 API/配置集成 |
| ThinkWatch Lite | MIT | ThinkWatch 可观测性 / 管理工具 | **允许直接复用**；优先与我们的 Admin UI 并行使用，避免重复实现底层 ThinkWatch 视图 |
| New API | AGPL-3.0 | Channel / Model 管理、Priority/Weight、Model Mapping、Usage/Quota、后台 UX | **默认不复制源码进本仓库**；参考概念、API 行为和 UX，必要时考虑独立服务集成 |
| Maskit | AGPL-3.0 | 隐私脱敏、代理安全、敏感数据审计 | **默认不复制源码进本仓库**；优先独立服务集成或参考设计 |

上游仓库：

- ThinkWatch Core: https://github.com/ThinkWatchProject/ThinkWatch-Core
- ThinkWatch Lite: https://github.com/ThinkWatchProject/ThinkWatch-Lite
- New API: https://github.com/QuantumNous/new-api
- Maskit: https://github.com/xiaYuTian11/maskit

> 本文是工程复用规范，不构成法律意见。复制第三方源码进入交付版本前，应由交付责任方确认 License 义务。

## 4. License 规则

Campus AI Gateway 当前仓库根目录没有记录正式 License 文件。

因此：

- 不允许静默导入 Copyleft 源码。
- MIT / BSD / Apache 风格源码即使可复制，也可能要求保留版权和 License 声明。
- AGPL / GPL 系列源码不能只因为“方便”就直接复制进 Gateway。
- 如果团队明确希望引入 AGPL 源码，应先做项目级 License 决策。
- 把上游项目作为独立服务/进程运行，通常比复制源码更容易保持架构边界，但仍需遵守其自身 License。

## 5. 能力复用映射

| 能力 | 优先上游 | 处理方式 |
| --- | --- | --- |
| Provider Routing | ThinkWatch Core | 直接复用 |
| Failover | ThinkWatch Core | 直接复用 |
| Circuit Breaker | ThinkWatch Core | 直接复用 |
| Guard / 路由安全基础 | ThinkWatch Core | 直接复用 |
| ThinkWatch Trace / 底层路由可视化 | ThinkWatch Lite | 尽量复用 |
| Channel Priority / Weight 思路 | New API | 参考行为和数据模型，不默认复制 AGPL 源码 |
| Model Mapping UX / 语义 | New API | 参考 |
| Quota / Usage 产品行为 | New API | 参考；Tenant 核算仍按 Campus Gateway 自己的控制面 |
| 敏感数据脱敏 | Maskit | 优先可选外部集成，不默认复制源码 |
| Metrics | Prometheus 生态 | 使用标准方案 |
| Distributed Tracing | OpenTelemetry | 使用标准方案 |
| RBAC | 成熟 Go RBAC 库 / 校园 IAM | 先复用再自研 |
| Load Testing | k6 / 现有压测工具 | 直接使用，不自研压测引擎 |

## 6. M1.5 复用决策

### M1.5-01 Model Route Schema

该 Schema 属于校园控制面自己的数据结构，用于连接：

- `gateway_model`
- `gateway_provider`
- 面向 Tenant 的虚拟模型
- ThinkWatch 配置渲染

New API 的 Priority / Weight / Channel 设计可用于参考，但其 AGPL 源码和数据结构不适合作为当前 PostgreSQL/pgx 控制面的直接拷贝对象。

### M1.5-02 Model Route CRUD API

实现原则：

- 复用 Campus Gateway 已有 Admin Handler / Storage 约定。
- 复用 M1.5-01 的 `gateway_model_route`。
- 真实 Route 执行继续交给 ThinkWatch。
- **不在 Go Gateway 中再实现路由引擎。**
- New API 只用于校准 Priority / Weight 等行为和后台 UX。
- 默认不复制 New API CRUD / Controller / Model 源码。

CRUD 层只保留校园控制面需要的轻量胶水逻辑。

### M1.5-03 Admin Route UI

优先复用现有 Embedded Admin UI。

可参考 New API 的：

- Priority 编辑
- Weight 编辑
- 启用/停用
- Model Mapping 展示
- 测试操作

参考交互，不直接复制 AGPL 源码。

### M1.5-05 / M1.5-06 Routing 与 Failover

除非 ThinkWatch 无法满足明确需求，否则**不要在 Campus Gateway 中自研 Failover**。

目标结构：

```text
Campus Control Plane
  -> 渲染 / 同步路由策略
  -> ThinkWatch Core
  -> 执行 Routing / Failover / Circuit Breaker
```

## 7. PR 要求

每个非小型功能 PR 必须包含：

```text
Open Source Reuse Check
- ThinkWatch:
- New API:
- Maskit:
- Other libraries:
- Directly reused:
- Not reused because:
- License notes:
```

Code Review 应拒绝没有充分理由的基础设施重复实现。

## 8. 架构原则

Gateway 应拥有的是**校园策略**，不是成熟项目已经实现的通用基础设施。

Campus Gateway 自己负责：

- Tenant
- Virtual API Key
- 学校 SSO 集成边界
- 校园模型权限
- 校园配额策略
- 校园审计要求
- ThinkWatch 控制面同步

优先交给成熟上游：

- Routing Engine
- Circuit Breaker
- Telemetry 标准
- 认证基础能力
- 通用 RBAC Engine
- Metrics / Tracing 标准
- Load Test Engine

## 9. 决策记录

| 日期 | 功能 | 评估上游 | 决策 |
| --- | --- | --- | --- |
| 2026-10-10 | 多 Provider Routing | ThinkWatch Core、New API | ThinkWatch 执行 Routing/Failover；Campus Gateway 负责 Route CRUD 控制面；New API 因 AGPL 与架构差异只作为设计参考 |
| 2026-10-10 | 隐私/安全代理 | Maskit | 保留为可选外部集成/设计参考；默认不导入 AGPL 源码 |
