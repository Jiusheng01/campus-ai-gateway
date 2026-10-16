# Admin UI 视觉与响应式优化报告（2026-10-16）

面向 `web/admin`（React 18 + TS + Vite，Go embed 后由 `/admin-ui` 提供）的界面显示优化。
目标形态：**桌面优先 + 移动可用**；桌面端保证信息密度与层级，780px 以下把侧栏转成抽屉、宽表横向滚动。

---

## 1. 调整范围

| 文件 | 说明 |
| --- | --- |
| `web/admin/src/styles.css` | 设计令牌、布局栅格、响应式断点、抽屉与表格交互提示（+208/−67） |
| `web/admin/src/app/Shell.tsx` | 移动端导航抽屉的状态与可访问性（+100/−?） |
| `web/admin/src/pages/ModelsPage.tsx` | 1 行：主线路标记 `isPrimary` |
| `internal/app/adminui/dist/index.html` | 构建产物占位（`dist/assets/` 按 `.gitignore` 不提交） |

**未改动**：其余页面组件、任何后端接口与字段、路由与权限逻辑。

---

## 2. 视觉样式

### 2.1 设计令牌

| 令牌 | 调整 |
| --- | --- |
| `--text-muted` / `--text-faint` / `--border` | 收敛到 slate 色阶（`#475569` / `#64748b` / `#e2e8f0`），提高正文与次要文字的对比 |
| 语义色 | `--success #16a34a`、`--warning #d97706`、`--info #0284c7`，并配套 `-soft` 底色 |
| 阴影 | 2 级 → 3 级 `--shadow-sm/md/lg`，浅色与深色各自定义（深色用纯黑叠加，浅色用 slate 叠加） |
| `--scroll-hint`（新增） | 表格横向滚动提示的阴影色，浅色 `rgb(15 23 42 / .18)`、深色 `rgb(0 0 0 / .55)` |

### 2.2 组件级

- `.kpiGrid` / `.statusRow` / `.providerCards`：由固定 `repeat(n, 1fr)` 改为 `auto-fit` / `auto-fill` + 最小轨道宽度，栅格随可用宽度自适应。
- `.kpi`：统一 16px 内边距，补 `border-color` / `box-shadow` 过渡与 hover 描边。
- `.routeStep.isPrimary .routeBox`：主线路加 3px 主色左边框，一眼分辨主/备线路。
- `.drawer`：`max-height: 100dvh`；遮罩改 `backdrop-filter: blur(2px)`，投影从 `-20px 0 50px -38px #000` 换成更克制的 `-12px 0 36px -10px rgb(0 0 0 / .35)`。

---

## 3. 响应式策略

断点只描述**流式栅格表达不了的变化**，不再重复 `repeat(n, 1fr)`（原先 1400 / 1180 / 900 / 480 四处重复覆盖已删除）。

| 断点 | 变化 |
| --- | --- |
| ≤1180px | `.filters` 3 列；`.settingsGrid` 单列；`.skeletonCards` 2 列 |
| ≤900px | `.desktopOnly` 隐藏 / `.mobileOnly` 显示 |
| ≤780px | 侧栏转 260px 离屏抽屉（`translateX(-100%)` → `.mobileShown` 滑入）；工作区 `margin-left: 0`；抽屉面板全屏 `100dvh` + `env(safe-area-inset-bottom)` 底部安全区；顶栏出现汉堡按钮、隐藏副标题与身份角色 |
| ≤620px | KPI 栅格转 2 列并收紧内边距与字号（`--kpiValue` 24px → 21px）；`.miniCardRows` 2 列 |
| ≤480px | 隐藏身份区；`.authCard` 收紧内边距 |

### 3.1 实测列数矩阵

| 视口 | KPI | 基础设施状态 | 供应商卡片 | 筛选器 |
| --- | --- | --- | --- | --- |
| 1440 | 4 | 4 | 3 | — |
| 1280 | 4 | 4 | 3 | — |
| 1180 | 3 | 3 | 2 | 3 |
| 1024 | 3 | 3 | 2 | 3 |
| 820 | 2 | 2 | 2 | 2 |
| 620 | 2 | 1 | 1 | 2 |
| 430 / 390 / 360 | 2 | 1 | 1 | 2 |

---

## 4. 交互与可访问性修复

### 4.1 抽屉的滚动锁泄漏（已修）

原先 `body.overflow = 'hidden'` 只随 `mobileNavOpen` 变化。抽屉开着时把视口拉宽到 780px 以上，侧栏回到固定定位，但**滚动锁不会解除**——整个桌面页面无法滚动，关闭按钮还会残留在侧栏里（`.mobileCloseBtn` 只有 `margin-left`，没有显示条件）。

修法：
- `Shell.tsx` 增加 `matchMedia('(min-width: 781px)')` 监听，视口变宽时主动关闭抽屉；
- `.sidebar .mobileCloseBtn` 默认 `display: none`，仅 ≤780px 显示。前缀 `.sidebar` 是必要的——`.iconBtn` 在样式表更靠后处声明了 `display: grid`，同权重下会按源序胜出。

### 4.2 表格横向滚动提示（新增）

宽表在窄屏下横向滚动，但原先没有任何提示：行在视口边缘被切断，看起来像渲染故障。现在用「贴在内容上的表面色渐变（`local`）遮盖贴在盒上的阴影渐变（`scroll`）」的标准手法，提示只在**还有内容的那一侧**出现，滚到该侧尽头自动消失。

### 4.3 其他

- 汉堡按钮补 `aria-controls="admin-sidebar"` / `aria-expanded`；`<aside>` 补 `id`。
- Escape 关闭抽屉、点击遮罩关闭、关闭后解除滚动锁（均已断言）。

---

## 5. 验收方法

不使用任何前端测试框架，直接对真实网关做原始 CDP 驱动：

1. **隔离环境**：起 `deploy-postgres-1` / `deploy-redis-1`，网关以 `ADMIN_AUTH_MODE=trusted-header` + `ADMIN_PROXY_SECRET` 启动，身份完全由请求头注入（`X-Campus-Auth-Secret` / `X-Forwarded-User` / `X-Forwarded-Groups: super_admin`）。
   - **不写任何用户数据**：不建账号、不改密码、不重置，只读既有开发库。
   - `THINKWATCH_CONFIG_PATH` 指向临时文件，避免改动本机 ThinkWatch 配置。
2. **驱动**：无头 Edge + `--remote-debugging-port`，Node 22 内置 `WebSocket` 直连 CDP，无 Playwright 依赖。
3. **判读**：主题与对比度**不靠肉眼**——自写 PNG 解码器（`zlib` + 逐行反滤波）按区域计算平均明度，栅格列数直接读 `getComputedStyle().gridTemplateColumns`。

### 5.1 覆盖

- 10 个页面 × 浅色 / 深色两轮全量截图（主题切换均以 `documentElement.dataset.theme` 断言，不信任点击）
- 9 档视口几何测量（1440 / 1280 / 1180 / 1024 / 820 / 620 / 430 / 390 / 360）
- 移动端：抽屉开启 / Escape 关闭 / 遮罩点击关闭 / 导航后自动关闭 / 全屏抽屉面板
- 回归：抽屉开启后拉宽视口 → 断言滚动锁解除、抽屉关闭、页面可滚动

### 5.2 结果

| 检查项 | 结果 |
| --- | --- |
| 10 页渲染与导航标题匹配 | 10/10，控制台 **0 异常** |
| 浅色 / 深色两轮主题确实不同 | 明度 246–250 vs 13–30 |
| 页面横向溢出（390px，5 个页面） | 0 |
| 抽屉开启：`transform` / 遮罩 / 滚动锁 / `aria-expanded` | `matrix(1,0,0,1,0,0)` / `block` / `hidden` / `true` |
| 抽屉关闭：Escape、遮罩点击、导航后 | 三者均关闭且滚动锁解除 |
| resize 回归（390 开抽屉 → 1440） | 锁 `hidden` → `''`，抽屉关闭，`canScroll: true` |
| 桌面宽度点击隐藏的汉堡按钮 | 无遮罩、无关闭按钮残留 |
| 全屏抽屉（390px） | `x=0, y=0, w=390, h=844`，`border-radius: 0` |
| 表格滚动提示 | 起点边缘比内侧暗 **22.76**（可见）；滚到末端差值 **0**（消失） |

---

## 6. 发现但未修的问题（需决策）

这两处不在本次约定的改动范围内（仅 `styles.css` / `Shell.tsx`），故未动，供决定：

1. **`/admin/users` 的降级态会显示误导性的 0**
   `UsersPage.tsx` 用 `stats?.total ?? 0` 渲染四张 KPI 卡。token / trusted-header 模式下该接口按设计返回 503，`stats` 为 `undefined`，于是「加载失败」旁边并排显示 `0 总用户 / 0 普通用户 / 0 管理员 / 0 停用账号`。
   建议：`stats` 存在时才渲染 KPI 区（或在失败时显示 `—`）。

2. **错误文案直接透出后端英文**
   同一页面把后端 `error.message`（`admin authentication is not configured`）原样显示在中文界面里。
   建议：按 `error.code`（`admin_api_disabled`）映射中文说明，例如「当前认证方式不支持账号管理，账号由校园统一身份认证平台维护」。

另有一处非缺陷的观察：请求日志表头 `Tokens` / `Request ID` 为英文，与同行的 `时间/状态/模型/供应商/延迟/故障切换` 混排，若追求一致性可改为「Token 数」等。

---

## 7. 复现

```bash
# 1. 依赖
docker compose -f ./deploy/docker-compose.yml -f ./deploy/docker-compose.dev.yml up -d postgres redis

# 2. 网关（trusted-header，只读既有开发库，ThinkWatch 配置指向临时文件）
export GOPROXY=https://goproxy.cn,direct
go build -o /tmp/gw-adminui.exe ./cmd/server
DATABASE_URL='postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable' \
REDIS_ADDR='127.0.0.1:6379' \
API_KEY_HMAC_SECRET='adminui-local-verification-secret-0123456789' \
ADMIN_AUTH_MODE='trusted-header' ADMIN_PROXY_SECRET='campus-ui-verify-secret' \
TRUSTED_PROXY_CIDRS='127.0.0.1/32' HTTP_ADDR=':8080' \
THINKWATCH_URL='http://127.0.0.1:18788' THINKWATCH_CONFIG_PATH='/tmp/tw-adminui-config.yaml' \
/tmp/gw-adminui.exe

# 3. 前端产物（改完必须重新 go build，dist 是编译期 embed 的）
npm --prefix web/admin run build
```

请求头：`X-Campus-Auth-Secret: campus-ui-verify-secret`、`X-Forwarded-User: ui-verifier`、`X-Forwarded-Groups: super_admin`。

---

## 附：验收截图

| 文件 | 内容 |
| --- | --- |
| `ADMIN_UI_POLISH_2026-10-16_light_dashboard.png` | 浅色总览（KPI 4 列、状态行 4 列） |
| `ADMIN_UI_POLISH_2026-10-16_dark_dashboard.png` | 深色总览 |
| `ADMIN_UI_POLISH_2026-10-16_light_models.png` | 模型与路由：主线路左侧强调边 |
| `ADMIN_UI_POLISH_2026-10-16_mobile_kpi.png` | 390px：KPI 两列 |
| `ADMIN_UI_POLISH_2026-10-16_mobile_nav_drawer.png` | 390px：导航抽屉滑入 |
| `ADMIN_UI_POLISH_2026-10-16_mobile_fullscreen_drawer.png` | 390px：抽屉面板全屏 |
| `ADMIN_UI_POLISH_2026-10-16_table_scroll_hint.png` | 窄屏表格右侧滚动阴影提示 |
