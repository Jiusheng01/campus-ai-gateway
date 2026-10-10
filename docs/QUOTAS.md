# 租户配额语义

Tenant 的配额字段保存在 `gateway_tenant`：

- `daily_token_limit`
- `monthly_token_limit`
- `monthly_request_limit`
- `monthly_budget_micros`

其中：

```text
0 = 不限制
```

## 1. 配额执行方式

月请求配额和日/月 Token 配额使用 Redis 共享 Key。

因此多个 Gateway 实例会共同使用同一组 Tenant 配额计数，而不是每个实例各算一份。

Token 流程：

```text
estimate
  -> 预占 Tenant 日/月 Token 配额
  -> 预占 API Key TPM
  -> 请求上游模型
  -> 获取实际 usage
  -> Tenant 配额对账
  -> TPM 对账
```

也就是：

```text
估算
  -> 预占
  -> 实际请求
  -> 实际 Token
  -> reconcile
```

如果 Provider / ThinkWatch 没有返回可信的实际 Usage，则保留请求前的估算值，不伪造“实际 Token”。

如果请求在真正到达上游前，被后续 Gateway 限制器拒绝，则已做的预占会被退款。

## 2. 时间边界

当前 Redis 配额 Bucket 使用 UTC 的日/月边界。

如果生产要求按 `Asia/Shanghai` 或学校财务自然日进行结算，应在最终部署前明确为正式业务规则并调整实现。

## 3. Budget

`monthly_budget_micros` 当前用于：

- 预算配置
- 后续报表基础
- 成本控制数据模型

目前**不会根据猜测价格直接做金额强制扣减**。

真正的金额配额执行需要：

1. 权威 Provider / Model 单价；
2. 实际使用的 Provider；
3. 实际 Upstream Model；
4. 实际 Token Usage；
5. Failover 后最终线路。

在这些数据可靠之前，不应通过估算价格冒充精确计费。
