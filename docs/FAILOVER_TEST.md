# Qwen → DeepSeek 故障转移验收

本文档用于 M1.5-06 的最终环境验收。

当前实现**不会在 Go Gateway 中重新实现一套 Failover 逻辑**。Campus AI Gateway 只负责生成有序的 Provider / Model 候选线路，实际运行时的故障转移和 Circuit Breaker 由 ThinkWatch Core 执行。

## 1. 前置条件

- Campus AI Gateway、PostgreSQL、Redis、ThinkWatch Core 均已启动。
- `campus-smart` 至少有两条启用中的 Route：
  1. Qwen，priority = 1
  2. DeepSeek，priority = 2
- ThinkWatch 运行环境中已配置：
  - `DASHSCOPE_API_KEY`
  - `DEEPSEEK_API_KEY`
- 已有可用的 `sk-campus-*` Key，并且允许调用 `campus-smart`。
- 当前数据库中的 Provider / Model / Route 配置已经同步到 ThinkWatch。

## 2. 验证方法

脚本默认不会直接执行，因为它会**临时把主 Provider 的 Base URL 改成不可访问的本地端口**，用于模拟主线路故障。

脚本使用 `finally` 恢复 Provider，并再次同步 ThinkWatch。

执行：

```powershell
$env:ADMIN_TOKEN = "<admin-token>"
$env:CAMPUS_API_KEY = "sk-campus-..."

.\scripts\verify-failover.ps1 -Execute
```

期望输出：

```text
[PASS] Primary was unreachable and the virtual-model request still succeeded.
[OK] Primary provider restored.
```

含义：

```text
业务系统
  -> 继续请求 campus-smart
  -> Qwen 主线路不可用
  -> ThinkWatch 自动尝试下一条 Route
  -> DeepSeek 返回成功
```

业务调用方不需要知道真实厂商发生了切换。

## 3. 安全说明

如果恢复主 Provider 失败，脚本会以退出码 2 结束，并打印明确的恢复错误。

这时请：

1. 在 Admin UI 中恢复 Qwen Provider 的正确 Base URL。
2. 执行“同步到 ThinkWatch”。
3. 确认 Qwen 恢复正常后再继续测试。

## 4. 当前验收状态

仓库已经具备：

- 多 Route 控制面
- ThinkWatch 多 Provider 配置渲染
- DeepSeek Provider 配置入口
- 可恢复的 Failover 验证脚本

但是，**真实 Qwen → DeepSeek Failover 仍需要最终部署环境和有效的 DeepSeek 凭据**。

在该脚本使用真实 Provider 成功跑通之前，不应把 M1.5 Failover 标记为“生产环境已验证”。
