# Admin UI v2

Campus AI Gateway 的管理后台已经从单文件 HTML 升级为独立前端工程，但最终仍由 Go Gateway 内嵌并通过 `/admin-ui` 提供。

## 技术栈

- React 18
- TypeScript
- Vite
- Lucide Icons
- 原生 CSS Design Tokens

Node.js 只在**构建阶段**使用。生产运行时不需要单独启动 Node 服务。

构建产物：

```text
web/admin
  -> npm run build
  -> internal/app/adminui/dist
  -> Go embed
  -> campus-ai-gateway
  -> /admin-ui
```

## 页面

- 总览
- Provider
- 模型与路由
- 租户
- API Key
- 请求日志
- 管理审计
- 安全事件
- 系统设置 / 当前身份

### 模型与路由

模型页把虚拟模型和真实线路分开表达。

示例：

```text
campus-smart
  -> Qwen / qwen3.7-flash... / P1
  -> Failover
  -> DeepSeek / deepseek-chat / P2
```

每条 Route 显示：

- Provider
- Upstream Model
- Priority
- Weight
- Timeout
- Status

编辑采用 Drawer，不再把表单插入列表正文。

### 请求日志

保留已有：

- 模型筛选
- Provider 筛选
- HTTP Status
- Request ID
- Failover
- Pagination
- CSV Export

点击一条请求后使用详情 Drawer 展示：

- 基础信息
- Routing
- Usage
- Timing

Route Telemetry 不存在时显示“未知”，不会根据计划 Route 猜测真实 Provider。

## 认证

### 本地开发

```text
ADMIN_AUTH_MODE=token
```

可以在“系统设置”保存本地 `ADMIN_TOKEN`。

### 生产

```text
ADMIN_AUTH_MODE=trusted-header
```

生产页面本身不要求 localStorage 中存在 Token。

浏览器请求通过可信反向认证代理后，由代理注入校园身份 Header；Gateway 继续负责 RBAC。

## 开发

需要 Node.js 22+。

```powershell
npm --prefix .\web\admin install
npm --prefix .\web\admin run check
npm --prefix .\web\admin run build
```

日常使用 `scripts/dev.ps1` 时会自动构建 Admin UI。

## Docker

`deploy/Dockerfile` 使用独立 Node Build Stage 编译 Admin UI，再进入 Go Build Stage。

最终运行镜像中不包含 Node.js。

## CI

CI 顺序：

```text
npm install
  -> TypeScript check
  -> Vite build
  -> go test ./...
  -> go build ./...
```

这样可以保证 Go Embed 使用的是当前前端源码生成的产物。

## 开源参考

本轮 UI 调研了：

- ThinkWatch Lite
- New API
- Maskit

采用规则：

- ThinkWatch Lite：MIT，可在 License 要求允许的范围内复用；本轮主要借鉴 Route / Traffic 信息架构。
- New API：AGPL-3.0，仅参考 Channel、DataTable、Drawer 等交互模式，不复制源码。
- Maskit：AGPL-3.0，仅参考侧栏、状态 Dashboard、深浅主题等视觉/交互模式，不复制源码。

最终设计方向：

> Maskit 的整体后台质感 + New API 的管理交互 + ThinkWatch Lite 的路由/请求表达。

## 后续可继续增强

Admin UI v2 当前重点是建立稳定、可维护的前端架构。后续可在不改变后端契约的情况下继续增加：

- 趋势图
- P50 / P99 图表
- Route Graph Hover 联动
- Provider Circuit Breaker 实时状态
- 更完整的移动端 Drawer
- Tenant Top N
- 服务端游标分页
- 日志时间范围筛选
