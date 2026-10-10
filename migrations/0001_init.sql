CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS gateway_tenant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code VARCHAR(64) NOT NULL UNIQUE,
  name VARCHAR(128) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS gateway_api_key (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES gateway_tenant(id),
  name VARCHAR(128) NOT NULL,
  key_prefix VARCHAR(32) NOT NULL,
  key_digest VARCHAR(128) NOT NULL UNIQUE,
  status SMALLINT NOT NULL DEFAULT 1,
  expire_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  allowed_models JSONB NOT NULL DEFAULT '[]'::jsonb,
  allowed_ips JSONB NOT NULL DEFAULT '[]'::jsonb,
  rpm_limit INT NOT NULL DEFAULT 60,
  tpm_limit BIGINT NOT NULL DEFAULT 100000,
  max_concurrency INT NOT NULL DEFAULT 10,
  route_group VARCHAR(64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_gateway_api_key_tenant ON gateway_api_key(tenant_id);

CREATE TABLE IF NOT EXISTS gateway_request_log (
  id BIGSERIAL PRIMARY KEY,
  request_id VARCHAR(64) NOT NULL UNIQUE,
  tenant_id UUID,
  api_key_id UUID,
  client_ip VARCHAR(64),
  requested_model VARCHAR(128),
  actual_model VARCHAR(128),
  provider VARCHAR(128),
  stream BOOLEAN NOT NULL DEFAULT FALSE,
  input_tokens BIGINT DEFAULT 0,
  output_tokens BIGINT DEFAULT 0,
  total_tokens BIGINT DEFAULT 0,
  latency_ms BIGINT DEFAULT 0,
  status_code INT,
  error_code VARCHAR(64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_request_log_created ON gateway_request_log(created_at DESC);
