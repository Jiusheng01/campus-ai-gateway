# 性能验收

采购目标为：

```text
30,000 RPM ≈ 500 requests/second
```

仓库已提供可重复执行的 k6 场景，但只有在代表性部署环境中产生并保存真实 Benchmark 结果后，才能把 30,000 RPM 标记为已通过。

## 1. 所需环境

尽量使用与生产一致的拓扑：

- 负载均衡器 / 反向代理
- 计划中的 Gateway 实例数量
- 生产级 PostgreSQL 和 Redis
- ThinkWatch Core
- 真实或可控的上游模型服务
- 已启用监控

不要使用笔记本本地 Loopback 测试结果直接宣称生产容量。

## 2. 测试场景

### 2.1 模型鉴权路径

```powershell
$env:BASE_URL = "https://gateway.example.edu"
$env:API_KEY = "sk-campus-..."
$env:TARGET_RPS = "500"
$env:DURATION = "2m"
k6 run .\loadtest\k6-models.js
```

### 2.2 非流式 Chat 路径

```powershell
$env:BASE_URL = "https://gateway.example.edu"
$env:API_KEY = "sk-campus-..."
$env:MODEL = "campus-smart"
$env:TARGET_RPS = "500"
$env:DURATION = "2m"
k6 run .\loadtest\k6-chat.js
```

还应分别测试：

- SSE
- 限流突发流量
- Qwen → DeepSeek Failover

真实 Provider 往往有配额、QPS 和成本限制。因此 Gateway 容量测试建议优先使用可控上游，再单独做较小规模真实 Provider 验收。

## 3. 每次压测必须记录

- Git Commit SHA
- 部署拓扑 / 实例数量
- 目标 RPS 与实际 RPS
- P50 / P95 / P99
- 错误率
- CPU / 内存
- Go goroutine 数量
- PostgreSQL 连接数 / 查询延迟
- Redis OPS / 延迟
- ThinkWatch 资源使用
- 上游模式：真实 / 可控
- 流式 / 非流式
- Failover 是否启用

## 4. 通过标准

30,000 RPM 只有在可重复测试中，Gateway 边界持续达到至少 500 RPS、错误率满足约定目标，并且不违反响应时间要求时，才能认为通过。

k6 中的 Threshold 只是自动化护栏，不能单独作为生产性能证明。

## 5. 当前状态

**压测工具已就绪；生产环境 Benchmark 待最终统一验收。**
