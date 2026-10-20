# MS1：模型服务管理上线、迁移与回退

[← 返回 README](../README.md) · 模型服务管理重构

本页是 MS1（New API 风格模型服务管理闭环）的管理员操作、迁移和回退说明。MS1 只覆盖上游渠道、候选模型、对外模型、路由、调用管理与日志闭环；Embedding（MS2）、ASR（MS3）和生产容量验证（MS4）不在本轮。

## 1. 管理员操作闭环

1. **上游渠道**：新增或编辑渠道，填写名称、供应商类型、协议、Base URL、凭据来源（inline 或 env）、状态和描述。凭据只写不读；编辑时留空表示保留原值。
2. **渠道诊断测试**：在渠道详情「调用测试」中填写上游模型和测试内容。默认结果只返回状态、耗时、模型和 Token；回答内容预览是单独的超级管理员操作，不持久化，并可能产生真实上游费用。
3. **模型列表**：在渠道详情「模型列表」中从上游刷新，或手动添加上游模型 ID / endpoint ID。刷新只新增，不删除已有名称，不覆盖来源。移除候选不会删除线路。
4. **发布对外模型**：从候选模型点击「发布为对外模型」，填写 alias、显示名称和描述。发布原子创建模型与首条线路；相同 alias 的不同绑定必须显式勾选「追加为备用线路」。
5. **路由策略**：在「路由策略」或模型详情中编辑整组线路。主备模式按顺序生成 priority 1..N；加权模式在同一 priority 内分配权重；高级模式直接编辑。模式切换涉及优先级、权重、超时或状态改变时，必须确认精确差异。保存使用 `expected_version`，冲突返回 409，草稿保留。
6. **授权**：超级管理员在模型详情「授权」中选择具体 API Key。服务端在最新白名单上原子增删，不会覆盖其他模型的授权；通配符 Key 不会被静默修改，需到 Key 页面显式转为白名单。
7. **真实业务调用**：使用校园 API Key 调用 `/v1/chat/completions`。`GET /v1/models` 只返回“Key 允许且当前静态可路由”的 alias。
8. **使用记录**：渠道/模型详情显示脱敏运行摘要（不含租户 ID、API Key ID、客户端 IP、问题或回答正文）。原始调用日志与完整 attempt chain 仅超级管理员和审计员可读。

## 2. 迁移 0017

`0017_model_service_management.sql` 是 MS1 唯一的 schema 变更，纯 additive：

- `gateway_provider`：`provider_type`、`description`、最近一次诊断测试的精简元数据（时间、成功/失败、HTTP 状态、耗时、上游模型、错误分类）。
- `gateway_model`：`display_name`、`description`、`config_version`。
- 索引：`gateway_request_log(requested_model, created_at DESC)`、`gateway_request_attempt(provider, created_at DESC)`。

不新增业务表，不修改历史 migration，不迁移或轮换凭据，不改变 `gateway_model_route` 的唯一绑定权威。

### 升级步骤

1. 备份数据库（至少 schema + 业务表）：
   ```bash
   pg_dump "$DATABASE_URL" -Fc -f campus-ai-gateway-pre-ms1.dump
   ```
2. 部署 MS1 镜像（包含 `migrations/0017_model_service_management.sql`）。Compose 的 `migrate` 服务会在 Gateway 启动前执行迁移。
3. 核对：
   ```bash
   psql "$DATABASE_URL" -c "SELECT version, filename FROM schema_migrations ORDER BY version DESC LIMIT 2;"
   psql "$DATABASE_URL" -c "SELECT config_version, display_name FROM gateway_model LIMIT 5;"
   ```
4. 用一个既有 Key 调用一次合法业务请求，确认日志仍写入、模型仍可路由。
5. 在管理台确认：渠道列表、候选模型、对外模型、路由策略、授权和脱敏摘要均可读取。

迁移测试覆盖全新库、pre-0017 存量库升级、重复启动和数据保留（见 `internal/migrate/migrate_0017_test.go`）。

## 3. 回退：必须按应用回退与数据库回退分开处理

### 3.1 为什么不能只 `git revert`

数据库已经记录 `0017` 后，pre-MS1 二进制的迁移 runner 会在启动时失败：

```text
version 17 (0017_model_service_management.sql) is recorded but not in the directory
```

这是 fail-closed 的迁移历史保护，不是可以忽略的警告。`git revert` 只回退代码，不会把 `schema_migrations` 和新增列一起回退，因此**不能当作完整回退**。

### 3.2 方案 A：应用回退，保留数据库（推荐用于短期回滚）

在隔离环境演练过两种路径：

1. **上一版本应用 + MS1 migration 目录**：使用上一版本代码构建的镜像，但把当前 `migrations/`（含 0017）挂载/复制进去。旧代码只查询自己需要的列；旧 schema contract 不要求新列，因此能通过最终校验并启动。命令示例：
   ```bash
   git checkout <pre-ms1-sha>
   cp /path/to/ms1-branch/migrations/0017_model_service_management.sql migrations/
   go build ./cmd/server
   ```
   Docker 部署时把 MS1 的 `migrations` 目录挂载到镜像内的 `/migrations`，并保持 `-migrations /migrations`。
2. **前向修复**：如果回退原因是 MS1 代码缺陷，优先部署修复后的 MS1 镜像；数据库不需要回退。

**限制**：旧二进制不会递增 `config_version`。在旧代码与新代码混合写入期间，整组保存的版本保证不完整；不要让旧二进制长期参与路由配置写入。

### 3.3 方案 B：数据库恢复（完整回退）

1. 停止 Gateway 写入。
2. 从升级前备份恢复：
   ```bash
   pg_restore --clean --if-exists --no-owner -d "$DATABASE_URL" campus-ai-gateway-pre-ms1.dump
   ```
3. 部署 pre-MS1 镜像。
4. 验证 `schema_migrations` 最高版本为 16，且既有 Key/alias/线路可调用。

数据库恢复会丢失升级后产生的请求日志、last-test 元数据和配置变更；这是完整回退的代价，不能把它描述成无损操作。

### 3.4 不要执行的“回退”

- 不要手工删除 `schema_migrations` 中的 0017 来骗过 runner；那会让数据库声明一个它并不处于的状态。
- 不要把 `DROP COLUMN` 当作应用回退的必需步骤；它不可逆，且会丢失 display/version/last-test 元数据。

## 4. 生效窗口与缓存

- 保存成功后，**保存操作的实例**下一次请求会重建路由表（写后失效）。
- 其他实例按现有 **5 秒 TTL** 收敛。
- 数据库故障时，实例保留旧路由表继续服务；`/admin/status` 的 `route_cache` 会显示 `stale`、快照时间、代次和脱敏错误分类。此时不能宣称“配置已全局生效”。
- 路由表从单个只读 repeatable-read 快照构建；写后失效使用代次保护，进行中的旧重建不会覆盖新配置。

## 5. 兼容性说明

- **alias 服务端不可变**：`PUT /admin/models/{id}` 修改 alias 返回 `400 alias_immutable`，即使使用旧 payload 形状。需要新名称时发布新 alias，并迁移调用方和 Key 白名单；MS1 不自动改写。
- **`GET /v1/models` 行为变化**：受限 Key 返回“静态可路由 ∩ allowlist”，不再返回原始 allowlist 中已禁用或不存在的 alias。通配符 Key 仍返回静态可路由集合；空白名单返回空列表。
- **旧 API 仍可用**：Provider/Model/Route 的旧路径与字段保持 additive；旧单条 Route CRUD 仍工作，并与整组保存共用 model 行锁和版本递增。
- **旧二进制限制**：旧二进制不递增 `config_version`，且不能在记录 0017 的数据库上直接启动；见 §3。
- **不持久化内容**：新测试、摘要和授权都不保存 prompt、answer、音频或真实密钥。

## 6. 采购要求对应矩阵（MS1）

| 要求 | MS1 处理 | 状态 |
| --- | --- | --- |
| REQ-PDF-02 系统管理第 5 条：自定义添加配置 AI 助手所需的大语言模型 | 渠道 → 候选 → 对外模型 → 路由 → Key 授权 → 调用 → 日志闭环 | 本轮实现 |
| REQ-PDF-01 第 5 页：模型选择与温度/最大输出/top P | 网关提供模型服务与参数透传边界；助手侧决定场景参数 | 保持边界，不新增全局默认参数 |
| REQ-PDF-03 文本生成 3 年 API 服务 | 软件能力与商务承诺分开；MS1 不宣称供应商/年限已验收 | 商务/交付待办 |
| REQ-PDF-04 Embedding / 向量相似度 | MS2，不在本轮 | 后续 spec |
| REQ-PDF-05 ASR 3 年服务 | MS3，不在本轮 | 后续 spec |
| REQ-PDF-06 3 万在线 / 30000 RPM / 5 秒 | MS4 定义场景和验证；本轮不宣称 | 后续验证 |
| REQ-PDF-07 HTTPS、加密、本地部署 | 保留现有出站安全、凭据加密和 RBAC；知识文件存储归知识库组件 | 保持边界 |
| REQ-PDF-08 全量问答日志 | 网关保留调用元数据；全量内容属于助手会话管理 | 不在本轮 |

## 7. 验证命令

```bash
go test ./... -count=1
go vet ./...
go build ./...
npm --prefix web/admin run check
npm --prefix web/admin run test
npm --prefix web/admin run build
```

`go test -race ./internal/app ./internal/storage` 需要 CGO 和 C 编译器；本机 Windows 环境缺少 gcc 时无法执行，CI 的 Ubuntu job 已加入该步骤。任何 SKIP 都必须单独记录，不能当作 PASS。
