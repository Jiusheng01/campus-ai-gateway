# Admin UI v3 —— 管理后台产品化重构报告

日期：2026-10-13
分支：`feature/admin-ui-v3`
基线：`main` @ `e9c2f083f8c0b271040174566fb769da3cb072af`

---

## 1. 结论

```text
Admin UI check                 PASS
Admin UI build                 PASS
Go test / vet / build          PASS
dev.ps1 READY                  PASS

Session login                  PASS
Refresh session                PASS
Trusted-header regression      PASS

Dashboard / Providers / Models & Routes   PASS
Tenants / API Keys / Request Logs         PASS
Admin Audit / Security Events / Settings  PASS

ThinkWatch Sync                PASS
Model Connectivity Test        PASS
CSV Export                     PASS

Light / Dark theme             PASS
1920 / 1440 / 1280 / Mobile    PASS

Native confirm() remaining     0
Credential localStorage        0

Backend behavior changed       NO
Quota changed                  NO
Routing algorithm changed      NO
Database migration added       NO
```

---

## 2. 做了什么

把「开发工具型后台」重构为可交付给学校信息化管理员长期使用的基础设施管理后台：
统一设计系统、侧栏按用途分组、Dashboard 三层信息结构、模型线路卡片化、请求日志
排障工作台化，并删除全部浏览器原生对话框。

### 2.1 代码结构

原 `App.tsx` 单文件 730 行同时承担启动流程、外壳、九个页面、所有表单抽屉、
所有详情视图与认证页面。现拆为：

```text
web/admin/src/
  app/         App.tsx（仅装配）· Shell.tsx · navigation.ts
  auth/        LoginPage · SetupPage · IdentityRequired · AuthSplash · AuthUnsupported · AccountSecurity
  pages/       九个页面各一个文件
  components/
    ui/        Button Badge Panel Table Drawer Modal Toast ConfirmDialog Field Detail EmptyState Skeleton PasswordInput
    providers/ models/ tenants/ keys/ logs/
  lib/         format.ts · useResource.ts · labels.ts
```

最大单文件 296 行（`ModelsPage.tsx`）。全仓 `any` 仅剩 1 处，位于未改动的 `api.ts`。

### 2.2 设计系统

`styles.css` 重建为单一变量集：`--bg / --surface / --surface-subtle / --text /
--text-muted / --border / --primary / --success / --warning / --danger /
--radius-sm|md|lg / --shadow-sm|md`，统一控件高度、圆角与间距。

- **字号**：旧样式表最低到 8px / 9px。现正文 13px、表格 12.5px、次要文字 11.5px。
- **按钮**：`primary / secondary / ghost / danger` × `sm / md`，含 `disabled` 与 `loading`。
- **暗色**：重新调色而非反相 —— 边框保持可见、muted 文字维持对比、warning/danger 不刺眼。
- 未引入任何 UI 框架，未新增 npm 依赖（仍为 react / react-dom / lucide-react）。
- `prefers-reduced-motion` 下关闭动画。

### 2.3 侧栏与 Topbar

九个菜单平铺改为按用途分组：**概览 / 模型服务 / 访问管理 / 可观测性 / 系统**。
折叠态只显示图标并保留 `title`；Active 态用左侧细条 + 主色底。

Topbar 左侧为页面标题 + 一句上下文；右侧为身份、主题、退出。身份区只显示主角色
加 `+N`（实测 `zhang.san / 审计员+1`），不再把五个角色字符串挤在顶部。刷新状态
由常驻文字「正在刷新」改为轻量 spinner。

### 2.4 Dashboard

三层结构，自上而下阅读：

1. **核心指标**：今日请求、今日 Tokens、错误率、P95 延迟。错误率由
   `today_errors / today_requests` 前端推导，**未新增后端接口**。
2. **基础设施状态**：Gateway / PostgreSQL / Redis / ThinkWatch，绿=正常、红=异常、灰=未知。
3. **供应商状态**：表格形式（供应商 / 请求数 / 成功率 / 平均延迟）。

故障切换次数降为供应商面板的脚注，不再与核心指标同权重。Quick Actions 统一置于
顶部：`同步 ThinkWatch`、`模型连通测试`，同步走统一 ConfirmDialog。

### 2.5 供应商页

每张卡片显示：友好名称 → 存储标识 → 状态 → 接口协议 → Base URL（截断，hover 显示
完整值）→ 凭据环境变量（monospace）。停用供应商整卡视觉降级。

模板为数据驱动常量，共 7 个：自定义 / 百炼 / DeepSeek / OpenAI / GLM / Kimi /
火山方舟。数据库里存为 `volcengine-ark` 的供应商通过别名表仍显示为「火山方舟 / 豆包」。
**编辑已有供应商时模板选择器整体隐藏**，模板不可能覆盖在用的配置。仍然没有任何
「真实厂商 API Key」输入框。

### 2.6 模型与路由页

每个对外模型一张卡片：`campus-smart` + 状态 + 「对外模型」+ `model=campus-smart`，
右侧 `模型测试` / `编辑模型`。下方为线路链：

```text
① 阿里云百炼 / 通义千问          主线路 · 启用
   qwen3.7-flash-2026-07-15
   优先级 1 · 流量权重 100 · 超时 60000 ms

   ↓ 失败自动切换

② 火山方舟 / 豆包                备用线路 · 启用
   doubao-seed-2-0-lite-260428
   优先级 2 · 流量权重 100 · 超时 60000 ms
```

「主线路」标记的是**第一条启用**的线路，排序仍严格依据 priority。后端未提供实时线路
健康数据，因此**不显示任何健康指示灯**。删除线路走 ConfirmDialog。

### 2.7 模型连通测试

原「模型测试」实际链路是 Admin API → ThinkWatch → Provider，**不是**业务数据面，
不消耗虚拟 Key、不占租户配额、不写 `gateway_request_log`。现改名为「模型连通测试」
并在抽屉内明确说明，结果页补一句「此结果不会出现在业务请求日志中」。

### 2.8 租户 / API Key

租户页桌面端改为表格（租户 / 编码 / 状态 / 三项配额 / 操作），≤900px 切回卡片。
用量按需拉取：点击「查看用量」才请求 `/admin/tenants/{id}/usage`，不为填一列而给
每一行发请求。

API Key 页表格化，Key 前缀 monospace。创建成功后进入专用一次性展示：明文只存在于
该组件 props，勾选「我已经安全保存此 Key」后「完成」才可点击，未勾选就关闭会二次确认。
**不写入 localStorage / sessionStorage / IndexedDB**。

### 2.9 请求日志

筛选栏（模型 / 供应商 / HTTP 状态 / Request ID / 故障切换）+ `查询` `重置` `导出 CSV`。
表格：时间 / 状态 / 模型 / 供应商 / Tokens / 延迟 / 故障切换 / Request ID。
状态徽章按 2xx 绿、4xx 橙、5xx 红分级；Request ID 等宽且可点击复制。

详情抽屉分为 **请求 / 路由 / 用量 / 时延 / 结果** 五段。ThinkWatch 未上报的遥测字段
继续显示「未知」，不推测。

### 2.10 统一对话框

删除全部 `window.confirm()`（原用于删除线路、同步 ThinkWatch、撤销设备）。
`ConfirmDialog` 基于 Promise，与 `Modal` 共用同一实现：

```text
同步 ThinkWatch 配置？   →  取消 / 确认同步
删除备用线路？           →  取消 / 删除线路（danger）
撤销该设备的登录状态？   →  取消 / 撤销设备（danger）
```

危险动作使用 danger 样式并锁定遮罩，避免误点。

### 2.11 Loading / Empty / Error

首次加载用 Skeleton（表格骨架 / 卡片骨架），不再整页显示「正在刷新」。空数据统一
`EmptyState` 并带下一步操作。失败提供「重新加载」；**403 单独处理**为「没有访问权限」
且不提供重试按钮 —— 权限拒绝重试没有意义。

### 2.12 响应式

| 视口 | 结果 |
|---|---|
| 1920×1080 | 侧栏 232px，KPI 四列，无横向溢出 |
| 1440×900 | 侧栏 232px，KPI 四列，无横向溢出 |
| 1280×720 | 侧栏 232px，KPI 四列，无横向溢出 |
| 768 | 侧栏 62px（图标），KPI 两列，表格切卡片，无横向溢出 |
| 390 | 侧栏 62px，KPI 单列，线路链纵向，无横向溢出 |

### 2.13 可访问性

图标按钮均带 `aria-label` + `title`；表单 label 正确关联；Modal / Drawer 支持 ESC
关闭并做基础 focus 管理；颜色不作为唯一状态信号 —— 绿点旁始终有「正常 / 启用」文字。

---

## 3. 证据

### 3.1 自动化

```text
npm --prefix web/admin run check     PASS
npm --prefix web/admin run build     PASS
go test -count=1 ./...               PASS
go vet ./...                         PASS
go build ./...                       PASS
.\scripts\dev.ps1 -ForceRestart -NoBrowser   → READY / EXITCODE=0
```

`dev.ps1` 日志：`Admin UI v2 built` → `Go tests passed` → `Gateway is healthy on :8080` → `READY`。

6 个提交逐个 `tsc -b --force` 通过。

### 3.2 浏览器实测

用无头 Edge + CDP 驱动真实界面：走真实登录表单登录，逐页读取渲染后的 DOM，并截图。

```text
login                         ok（真实表单输入 + 提交）
导航分组                       概览 / 模型服务 / 访问管理 / 可观测性 / 系统
导航项                         总览 供应商 模型与路由 租户 API Key 请求日志 管理审计 安全事件 系统设置
Topbar                        总览 · 校园 AI 基础设施运行状态
身份                            campus-admin / 超级管理员
localStorage                   {"campus_theme":"light"}   ← 无任何凭据键

Dashboard KPI                  今日请求 4 · 今日 Tokens 621 · 错误率 0.00% · P95 3.8 s
基础设施                        Gateway 正常 / PostgreSQL 正常 / Redis 正常 / ThinkWatch 正常
供应商页按钮                    ["新增供应商"]，无「添加 DeepSeek」按钮
供应商卡片                      qwen→阿里云百炼 / 通义千问，volcengine-ark→火山方舟 / 豆包
供应商模板                      7 个选项；DeepSeek / 百炼 预填正确；自定义 → 空白表单
真实 API Key 输入框             false
模型卡片                        campus-smart · 对外模型 · model=campus-smart
线路链                          [主线路·启用] qwen3.7-flash-2026-07-15 优先级1/权重100/超时60000
                               [备用线路·启用] doubao-seed-2-0-lite-260428 优先级2/权重100/超时60000
                               连接符「失败自动切换」· 按钮「添加备用线路」
残留术语                        VIRTUAL MODEL / 虚拟模型 Alias / Provider / Failover 均为 0
模型连通测试抽屉                标题「模型连通测试」+ 数据面说明
租户表格列                      租户 编码 状态 日 Token 配额 月 Token 配额 月请求配额 操作
API Key 表格列                  名称 租户 Key 前缀 状态 允许模型 RPM TPM 并发 最后使用 操作
请求日志筛选                    模型 供应商 HTTP 状态 Request ID 全部故障切换 + 查询/重置
请求日志表格列                  时间 状态 模型 供应商 Tokens 延迟 故障切换 Request ID
安全事件表格列                  时间 事件 租户 API Key 来源 IP 模型 详情
设置面板                        当前管理员 / 修改密码 / 登录设备 / 认证方式
登录状态文案                    安全会话 · 自动续期
密码显隐按钮                    3 个，aria-label「显示密码」
主题切换                        #f4f6fa → #0b0e14，data-theme=dark，localStorage=dark
Console Error / Page Error      0（干净环境下）/ 0
```

`403` 权限拒绝渲染为：`没有访问权限` + 说明文字，`hasRetryButton: false`。

租户页断点：

```text
1440px → .desktopOnly display:block（表格 2 行），.mobileOnly display:none，无横向溢出
 390px → .desktopOnly display:none，.mobileOnly display:block（2 张卡片），无横向溢出
```

### 3.3 业务回归

在**独立临时数据库**（`campus_gateway_uiv3`，与用户库同构：qwen + volcengine-ark、
campus-smart、两条线路、2 租户 2 Key）上实测，**未触碰用户数据库**：

```text
GET /v1/models                          200  {"id":"campus-smart"}
campus-smart BEFORE sync                200
POST /admin/thinkwatch/sync             200  {"providers":2,"aliases":1,"ok":true}
campus-smart AFTER sync                 200
SSE AFTER sync                          200
request log provider                    'qwen'（真实落库）
                                        upstream_model / route_priority / attempts 仍为未知
```

同步后 ThinkWatch 配置文件与验收前备份 **逐字节一致**，证明 renderer 行为未变。

### 3.4 trusted-header 回归

`ADMIN_AUTH_MODE=trusted-header` 实例（:8091）：

```text
无请求头                                  401
仅伪造 X-Forwarded-User（无共享密钥）      401
错误共享密钥                              401
正确共享密钥 + 用户                       200
```

五角色 RBAC 矩阵与既有文档一致（`super_admin` 全通、`gateway_admin` 无租户写权限、
`tenant_admin` 无同步权限、`auditor` 可读安全事件、`operator` 仅状态与连通测试）。

UI 侧：注入请求头后直接进入后台，身份显示 `zhang.san / 审计员+1`；设置页显示
「当前管理员 / 账号与安全 / 认证方式」，**不显示修改密码与登录设备**，认证方式为
「学校统一身份认证」。

### 3.5 截图

`docs/screenshots/admin-ui-v3/`

```text
01-dashboard-light.png          08-dashboard-dark.png
02-providers.png                09-mobile-models.png
03-provider-templates.png       10-empty-state-providers.png
04-models-routes.png
05-model-test.png
06-request-logs.png
07-settings.png
```

---

## 4. 变更声明

```text
NO DATABASE SCHEMA CHANGE          （无新增/修改 migration）
NO API CONTRACT BREAK              （未新增、未修改任何 Admin API）
NO ROUTING BEHAVIOR CHANGE         （未改 ThinkWatch renderer / Failover 算法）
NO QUOTA CHANGE
NO RPM / TPM CHANGE
NO API KEY DATA-PLANE CHANGE
NO SESSION AUTH BACKEND CHANGE
```

改动范围仅：

```text
web/admin/**
internal/app/adminui/dist/**
docs/screenshots/**  docs/ADMIN_UI_V3_REPORT_2026-10-13.md
```

`git diff main...HEAD --stat` 中未出现 `internal/proxy`、`internal/ratelimit`、
`internal/storage`、`migrations`、`internal/app/*.go` 的任何改动。

### 提交

```text
88760c3 refactor(admin-ui): add shared UI primitives and formatting helpers
65c1439 refactor(admin-ui): add the console shell, navigation groups and auth screens
ab96447 feat(admin-ui): build the overview, supplier and model-route pages
3162755 feat(admin-ui): build the access, observability and settings pages
64a73fe refactor(admin-ui): wire the console together and replace the monolithic App
96801da build(admin-ui): rebuild the embedded bundle and add a favicon
```

---

## 5. 已知限制 / 遗留问题

- **启动时的 401 日志**：无会话时 `POST /admin/auth/refresh` 必然返回 401，这是
  「是否已登录」的唯一问法（Cookie 为 HttpOnly，前端无法读取）。浏览器会把它记为
  一条 `Failed to load resource`，不影响功能，也非应用错误。
- **无实时线路健康**：后端未提供，UI 不显示健康指示灯，仅展示配置。
- **模型连通测试仍非数据面测试**：需要业务 API Key 输入与后端配合，未在本轮实现。
- **`route_group` 字段**在 API Key 表单中标签为「线路分组」，字段名与 API 未变。
- 管理审计中 `/admin/password` 仍同时产生语义审计 `admin_password_change` 与中间件
  通用 `POST` 行，保持既有行为。

---

## 6. Future Work（本轮明确未做）

```text
Provider 真实「测试连接」API
完整 Data Plane 模型测试（需业务 API Key 输入与后端设计）
管理员账号 CRUD / 忘记密码
ADMIN_SESSION_IDLE_TTL
新图表 API / WebSocket 实时日志
新后端统计 API（含 Dashboard 聚合）
SSO 配置页面
Quota 新逻辑 / Route 新算法
```
