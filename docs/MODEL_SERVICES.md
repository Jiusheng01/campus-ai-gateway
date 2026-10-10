# 扩展模型服务

Campus AI Gateway 对不同模型服务保持一致的外部鉴权模型：客户端始终使用虚拟模型别名和 `sk-campus-*` Key，真实 Provider 模型名由控制面维护。

## Embeddings

`POST /v1/embeddings` 已接入现有 Gateway 流程，包括：

- JSON 请求解析
- 模型白名单校验
- RPM / TPM / 最大并发控制
- ThinkWatch 透明转发

真实 Embedding Provider / Model 仍需配置为 Provider + Model Route，并在最终环境中完成真实请求验收。

## ASR / 音频转写

`POST /v1/audio/transcriptions` 已在 Gateway 的传输与鉴权层支持：

- 接收 `multipart/form-data`
- 从 multipart 中提取 `model` 字段做模型权限校验
- 恢复原始 multipart 请求体后透明转发
- 请求体大小上限为 64 MiB
- 继续执行 Virtual Key / IP / RPM / TPM / 最大并发控制

Gateway 不实现语音解码或 ASR 推理本身。运行时集成继续由 ThinkWatch / Provider 协议适配层负责。

## 当前状态

Embedding 与 ASR 的 **Gateway 基础能力已实现**。

仍需最终环境验收：

- 真实 Embedding Provider 请求
- 真实 ASR Provider 请求
- 对应 Provider 协议兼容性
- Usage / Token / 审计行为

在真实请求成功前，不应把对应模型服务标记为“生产环境已验证”。
