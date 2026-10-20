export type ProviderTemplate = {
  id: string
  label: string
  values: {name: string; base_url: string; protocol: string; credential_env: string; provider_type?: string}
  /**
   * Additional stored identifiers this template should be recognised by when
   * labelling an existing supplier. The database keeps whatever name the
   * operator configured, so a supplier created as `volcengine-ark` must still
   * read as 火山方舟 / 豆包.
   */
  aliases?: string[]
}

/**
 * Supplier presets. Data-driven on purpose: adding a vendor must not mean
 * touching form logic. A template only prefills the form — every field stays
 * editable, and no real vendor API key is ever collected or stored.
 */
export const providerTemplates: ProviderTemplate[] = [
  {
    id: 'custom',
    label: '自定义 / OpenAI Compatible',
    values: {name: '', base_url: '', protocol: 'openai-chat', credential_env: '', provider_type: ''},
  },
  {
    id: 'qwen',
    label: '阿里云百炼 / 通义千问',
    values: {
      name: 'qwen',
      base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
      protocol: 'openai-chat',
      credential_env: 'DASHSCOPE_API_KEY',
      provider_type: 'qwen',
    },
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    values: {name: 'deepseek', base_url: 'https://api.deepseek.com/v1', protocol: 'openai-chat', credential_env: 'DEEPSEEK_API_KEY', provider_type: 'deepseek'},
  },
  {
    id: 'openai',
    label: 'OpenAI',
    values: {name: 'openai', base_url: 'https://api.openai.com/v1', protocol: 'openai-chat', credential_env: 'OPENAI_API_KEY', provider_type: 'openai'},
  },
  {
    id: 'glm',
    label: '智谱 GLM',
    values: {
      name: 'glm',
      base_url: 'https://open.bigmodel.cn/api/paas/v4',
      protocol: 'openai-chat',
      credential_env: 'ZHIPU_API_KEY',
      provider_type: 'glm',
    },
  },
  {
    id: 'kimi',
    label: 'Moonshot / Kimi',
    values: {name: 'kimi', base_url: 'https://api.moonshot.cn/v1', protocol: 'openai-chat', credential_env: 'MOONSHOT_API_KEY', provider_type: 'kimi'},
  },
  {
    id: 'doubao',
    label: '火山方舟 / 豆包',
    values: {
      name: 'doubao',
      base_url: 'https://ark.cn-beijing.volces.com/api/v3',
      protocol: 'openai-chat',
      credential_env: 'ARK_API_KEY',
      provider_type: 'doubao',
    },
    aliases: ['volcengine-ark', 'ark'],
  },
]

// The data plane dials channels itself and speaks one protocol: OpenAI chat.
// Offering the others here would be a trap — the gateway rejects them on save —
// so the picker lists only what can actually be saved.
export const PROTOCOLS = ['openai-chat'] as const

// Labels for a protocol already stored on a row. Kept wider than PROTOCOLS so a
// channel saved before the restriction still renders a name instead of a bare
// string.
const PROTOCOL_LABELS: Record<string, string> = {
  'openai-chat': 'OpenAI Compatible',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic Messages',
  gemini: 'Gemini',
}

export const protocolLabel = (p: string) => PROTOCOL_LABELS[p] || p || '未知协议'

/**
 * Friendly supplier name for the card header, derived from the stored
 * identifier. The database keeps whatever name the operator configured — this
 * only decides what the label above it reads.
 */
export function friendlyProviderName(name: string): string | null {
  const match = providerTemplates.find(
    t => t.id !== 'custom' && (t.values.name === name || (t.aliases || []).includes(name)),
  )
  return match ? match.label : null
}
