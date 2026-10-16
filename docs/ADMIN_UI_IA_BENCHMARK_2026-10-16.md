# Admin UI 信息架构对标：new-api

> 日期：2026-10-16
> 范围：**数据看板 / API 密钥 / 使用日志** 三块
> 结论：**可以按 new-api 的信息架构自研，但不得复制其源码。**

---

## 1. 许可前提（先读这一节）

`QuantumNous/new-api` 的许可证经 GitHub 官方识别为 **AGPL-3.0**（SPDX `agpl-3.0`），
且其 `NOTICE` 文件声明了 **AGPLv3 第 7 条附加条款**：

- 修改版必须在**适当法律声明**及界面上任何显著的 about / legal / footer / 署名位置，
  保留 `Frontend design and development by New API contributors.`
- 修改版若提供用户界面，必须在显著位置保留指向原项目的可见链接
- 修改版必须标注自己的改动（第 7(c) 条）

对本项目的具体影响：**AGPL 第 13 条是专为网络服务设计的**。Campus AI Gateway 本身
就是面向全校的网络服务，用户通过网络与之交互即触发「向这些用户提供对应源码」的义务。
因此「只在校园内网用、不分发」这一常见抗辩在此**不成立**。

本仓库 `docs/OPEN_SOURCE_REUSE.md` 已有既定决策，与本结论一致：

> New API | AGPL-3.0 | **默认不复制源码进本仓库**；参考概念、API 行为和 UX

同时该文档指出：本仓库根目录**没有 LICENSE 文件**，因此引入 Copyleft 前应先做项目级
许可决策。本次不做该决策，本方案**不引入任何第三方源码**。

**本文档的性质**：仅记录从公开仓库的**目录与文件命名**观察到的信息架构（有哪些页面、
有哪些筛选维度、有哪些统计口径），用于自研时确定功能范围。不含、也不应引入其代码。

---

## 2. 现状基线（本项目）

### 2.1 前端页面

| 页面 | 文件 | 规模 |
| --- | --- | --- |
| 数据看板 | `web/admin/src/pages/DashboardPage.tsx` | KPI 卡 + 基础设施状态 + 供应商状态表 |
| API 密钥 | `web/admin/src/pages/ApiKeysPage.tsx` | 146 行 |
| 请求日志 | `web/admin/src/pages/RequestLogsPage.tsx` | 169 行 |
| 管理审计 | `web/admin/src/pages/AuditPage.tsx` | 独立页面 |
| 安全事件 | `web/admin/src/pages/SecurityPage.tsx` | 独立页面 |

### 2.2 看板现有口径

`model.DashboardStats` 只有 6 个**当日标量**：

```
today_requests / today_tokens / today_errors / avg_latency_ms / p95_latency_ms / failover_count
```

加上 `ProviderStats{provider, requests, success_rate, avg_latency_ms}`。

**没有时间序列、没有日期区间、没有任何图表。** 这是与 new-api 最大的结构性差距。

### 2.3 后端聚合能力

`internal/storage/` 中与日志相关的仅有：

```
SearchRequestLogs / ListRequestLogs / InsertRequestLog
```

**没有任何 GROUP BY 或按时间分桶的聚合查询。**

`gateway_request_log` 现有字段：
`request_id, tenant_id, api_key_id, client_ip, requested_model, actual_model, provider,
upstream_model, route_priority, attempts, failover, stream, input_tokens, output_tokens,
total_tokens, latency_ms, status_code, error_code, created_at`

**没有成本/计费列，没有缓存命中列，没有分组列。**

### 2.4 日志筛选能力

`storage.RequestLogFilter` 仅支持：
`Model / Provider / Status / RequestID / Failover`

**不支持时间区间、租户、API Key、客户端 IP。**

### 2.5 密钥操作能力

`/admin/api-keys/{id}` **只接受 PUT**（编辑），其他方法返回 405。
即：**没有删除 / 吊销单个 Key 的端点**，前端「操作」列也只有「编辑」。

---

## 3. 逐项差距

### 3.1 数据看板

| 能力 | new-api | 本项目 | 差距 |
| --- | --- | --- | --- |
| 概览 KPI 卡 | ✅ `overview/summary-cards` | ✅ 已有 | — |
| 运行时长 / 健康面板 | ✅ `overview/uptime-panel`、`performance-health-panel` | 🟡 只有四组件布尔状态 | 缺可用率与趋势 |
| **消耗分布图** | ✅ `models/consumption-distribution-chart` | ❌ | **P0** |
| **模型维度图表** | ✅ `models/model-charts` | ❌ | **P0** |
| **用户维度图表** | ✅ `users/user-charts` | ❌ | **P0**（对应本项目「租户」） |
| 日志统计卡 | ✅ `models/log-stat-cards` | ❌ | P1 |
| 性能总览 | ✅ `models/performance-overview` | 🟡 供应商表 | P1 |
| 图表偏好设置 | ✅ `models-chart-preferences` | ❌ | P2 |
| 看板分节（多 section 路由） | ✅ `dashboard/$section.tsx` + `section-registry` | ❌ 单页 | P2 |
| 看板可配置 | ✅ `hooks/use-dashboard-config` | ❌ | P2 |
| 调用流向图 | ✅ `flow/flow-charts` | ❌ | 不建议（见 §5） |
| 公告 / FAQ / 上手引导 | ✅ `overview/announcements-panel` 等 | ❌ | 不建议 |

### 3.2 使用日志

| 能力 | new-api | 本项目 | 差距 |
| --- | --- | --- | --- |
| **日期时间区间选择** | ✅ `compact-date-time-range-picker` | ❌ 只能翻页 | **P0** |
| **表格上方统计汇总** | ✅ `common-logs-stats` | ❌ | **P0** |
| **按租户 / API Key 筛选** | ✅ `group-filter`、`user-info-dialog` | ❌ | **P0** |
| 按日志类型分表 | ✅ common / drawing / task 三套列 | ❌ 单表 | P1（本项目只有一种） |
| 导出可选字段 | ✅ `export-logs-dialog` | 🟡 固定字段全量 CSV | P1 |
| 失败原因详情 | ✅ `fail-reason-dialog` | 🟡 Drawer 内有 error_code | P1 |
| 请求/响应内容预览 | ✅ `prompt-dialog` | ❌ | 不建议（隐私） |
| 计费来源与成本显示 | ✅ `log-cost-display`、`billing-source` | ❌ 无成本列 | P1（需先有计价） |
| 时延分解指标 | ✅ `timing-metrics-cell` | 🟡 只有总 latency_ms | P2 |
| 审计日志查看器 | ✅ `audit/` 独立子模块 | ✅ `AuditPage.tsx` 已有 | — |
| 移动端卡片视图 | ✅ `usage-logs-mobile-card` | 🟡 表格横向滚动 | P2 |
| 批量操作 / 行内操作菜单 | ✅ `data-table-row-actions`、`bulk-actions` | 🟡 只有行内单按钮 | P1 |
| 表格列固定 / 视图模式切换 | ✅ `column-pinning`、`view-mode-toggle` | ❌ | P2 |

### 3.3 API 密钥

| 能力 | new-api | 本项目 | 差距 |
| --- | --- | --- | --- |
| 列表 + 新建 + 编辑 | ✅ | ✅ | — |
| **密钥额度显示** | ✅ `api-key-quota-cell` | ❌ 只有 RPM/TPM 上限，**无已用量** | **P0** |
| **删除 / 吊销 Key** | ✅ `delete-dialog` | ❌ **后端无端点** | **P0** |
| 批量删除 | ✅ `multi-delete-dialog` | ❌ | P1 |
| 批量操作栏 | ✅ `data-table-bulk-actions` | ❌ | P1 |
| Key 分组 | ✅ `api-key-group-cell` | 🟡 有 `route_group` 字段，未在 UI 体现 | P1 |
| 自动分组顺序 | ✅ `auto-group-order-editor` | ❌ | P2 |
| API 地址展示（供复制接入） | ✅ `api-addresses` | ❌ | P1 |
| 新建后一次性展示明文 | — | ✅ 已有 `SecretModal` | — |

---

## 4. 缺口分档

### P0 — 做了立刻有感（建议先做）

1. **看板时间序列**：新增按小时/天的 `requests / tokens / errors / latency` 聚合，
   配日期区间选择器与折线/柱状图。
2. **日志时间区间筛选 + 表格上方统计**：解决「翻页找数据」的核心痛点。
3. **日志按租户 / API Key 筛选**：多租户场景的刚需。
4. **密钥已用量列**：现有 RPM/TPM 只是「上限」，看不出「用了多少」。
5. **密钥删除 / 吊销端点**：当前只能编辑，无法停用一个 Key，属功能缺失。

### P1 — 有价值，可排期

6. 消耗分布（按模型 / 供应商 / 租户的占比）
7. 导出可选字段
8. 密钥批量操作与分组展示
9. 失败原因与重试链路可视化

### P2 — 锦上添花

10. 看板分节与图表偏好、列固定、视图模式切换、移动端卡片视图

### 不建议引入

| 能力 | 原因 |
| --- | --- |
| 调用流向图 | 与现有「模型与路由」页职责重叠，且实现成本高 |
| 公告 / FAQ / 上手引导 | 属社区运营功能，校园部署不需要 |
| 请求/响应内容预览 | 校园场景涉个人数据，留存与展示 prompt 有合规风险 |
| 钱包 / 兑换码 / 订阅 / 定价页 / 排行榜 / 聊天 / 演练场 | 属商业计费与 C 端产品线，与本项目定位不符 |

---

## 5. 落地要点

主要工作量在**后端聚合查询**，不在前端。

### 5.1 需要新增的聚合查询

| 查询 | 用途 |
| --- | --- |
| 按时间桶聚合 | 看板趋势图（`date_trunc('hour'\|'day', created_at)`） |
| 按维度聚合 | 消耗分布（按 `requested_model` / `provider` / `tenant_id` / `api_key_id`） |
| 区间汇总 | 日志页表格上方统计（同筛选条件下的 count/sum/avg/p95） |
| 按 Key 用量 | 密钥页已用量列 |

### 5.2 索引前置条件

现有索引只有 `idx_request_log_created(created_at DESC)`。
按维度聚合前需评估补充索引，否则大表上 `GROUP BY` 会全表扫描：

- `(created_at, tenant_id)`、`(created_at, api_key_id)`、`(created_at, provider)`

### 5.3 需扩展的筛选结构

`storage.RequestLogFilter` 增加 `From / To / TenantID / APIKeyID / ClientIP`，
并同步 `RequestLogsPage` 的筛选栏与 `/admin/request-logs` 的参数解析。

### 5.4 成本口径

本项目 `gateway_request_log` **没有成本列**。若要做成本展示，
需先定义计价模型（每模型单价）与落库方式，属独立议题，**不建议与 P0 混做**。

---

## 6. 附：对标信息来源

仅为**目录与文件命名**层面的公开观察，用于确定功能范围：

- 前端路由：`web/src/routes/_authenticated/{dashboard,keys,usage-logs}/`
- 功能模块：`web/src/features/{dashboard,keys,usage-logs,performance-metrics}/`
- 许可证：`LICENSE`（AGPL-3.0）、`NOTICE`（第 7 条附加条款）、`README.md`（License 段）
- 许可状态核验方式：GitHub License API 返回 `spdx_id: agpl-3.0`

> 本文是工程对标记录，不构成法律意见。任何复制第三方源码的决定，
> 应由交付责任方确认 License 义务。
