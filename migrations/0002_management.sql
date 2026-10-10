CREATE TABLE IF NOT EXISTS gateway_provider (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(128) NOT NULL UNIQUE,
  base_url TEXT NOT NULL,
  protocol VARCHAR(32) NOT NULL DEFAULT 'openai-chat',
  credential_env VARCHAR(128) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS gateway_model (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  alias VARCHAR(128) NOT NULL UNIQUE,
  provider_id UUID NOT NULL REFERENCES gateway_provider(id),
  upstream_model VARCHAR(128) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gateway_model_provider ON gateway_model(provider_id);
CREATE INDEX IF NOT EXISTS idx_gateway_provider_status ON gateway_provider(status);
CREATE INDEX IF NOT EXISTS idx_gateway_model_status ON gateway_model(status);
