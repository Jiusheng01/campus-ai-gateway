# Additional Model Services

Campus AI Gateway keeps the external authorization model consistent across model service types: clients use a virtual model alias and a `sk-campus-*` key; provider-specific model names stay behind the control plane.

## Embeddings

`POST /v1/embeddings` already uses JSON request parsing, model allowlist checks, RPM/TPM/concurrency controls and transparent ThinkWatch forwarding.

A real embedding provider/model must still be configured as a Provider + Model Route and validated in the final environment.

## ASR / audio transcription

`POST /v1/audio/transcriptions` is supported at the Gateway transport/auth layer:

- multipart/form-data is accepted
- the `model` field is extracted for model authorization
- the original multipart body is restored and transparently proxied
- request body limit for this endpoint is 64 MiB
- normal Virtual Key/IP/RPM/TPM/concurrency controls still apply

The Gateway deliberately does not implement speech decoding itself. ThinkWatch/provider protocol support is the runtime integration point.

## Status

Embedding and ASR **gateway foundations are implemented**. Vendor-specific ASR protocol adapters and real provider acceptance are environment/provider tasks and must not be reported as production-verified until a real request succeeds.
