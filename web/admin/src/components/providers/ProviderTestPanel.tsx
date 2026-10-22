import {useState} from 'react'
import {FlaskConical} from 'lucide-react'
import {api, type Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {Detail} from '../ui/Detail'
import {useToast} from '../ui/Toast'
import {ms} from '../../lib/format'

type Result = {
  channel: string
  upstream_model: string
  status_code: number
  latency_ms: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  content?: string
}

/**
 * Channel diagnostic. It is a management probe, not a business request: no
 * virtual API Key, no tenant quota, no request log. Answer content is a
 * separate super_admin-only preview and is never persisted.
 */
export function ProviderTestPanel({
  provider,
  canPreviewContent,
  onTested,
}: {
  provider: Provider
  canPreviewContent: boolean
  onTested: () => void
}) {
  const toast = useToast()
  const [upstream, setUpstream] = useState('')
  const [prompt, setPrompt] = useState('请只回复：MODEL_OK')
  const [maxTokens, setMaxTokens] = useState(64)
  const [includeContent, setIncludeContent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState('')

  const run = async () => {
    if (busy || !upstream.trim() || !prompt.trim()) return
    if (!Number.isInteger(maxTokens) || maxTokens < 1 || maxTokens > 1024) {
      setError('最大输出 Token 必须为 1 至 1024 的整数。')
      return
    }
    setBusy(true)
    setResult(null)
    setError('')
    try {
      const out = await api<Result>(`/admin/providers/${provider.id}/test`, {
        method: 'POST',
        body: JSON.stringify({
          upstream_model: upstream.trim(),
          prompt: prompt.trim(),
          max_tokens: maxTokens,
          include_content: includeContent,
        }),
      })
      setResult(out)
      toast.success('渠道诊断完成')
    } catch (e) {
      setError(e instanceof Error ? e.message : '渠道诊断失败，请重试。')
    } finally {
      setBusy(false)
      onTested()
    }
  }

  return (
    <div className="drawerStack">
      <Notice>
        <FlaskConical aria-hidden />
        该测试直接使用已保存的渠道地址与凭据，<b>不会</b>经过业务 API Key、租户配额或使用日志；可能产生真实上游费用。
        每实例同一操作员同时最多 1 个、每分钟最多 6 次。
        {provider.status !== 1 ? ' 当前渠道已停用，测试只验证连通性。' : ''}
      </Notice>

      <div className="form">
        <Field label="上游模型" hint="该渠道实际调用的模型 ID 或推理接入点 ID。">
          <input className="mono" disabled={busy} value={upstream} onChange={e => setUpstream(e.target.value)} placeholder="例如 qwen-plus 或 ep-…" />
        </Field>
        <Field label="测试内容">
          <textarea disabled={busy} rows={4} value={prompt} onChange={e => setPrompt(e.target.value)} />
        </Field>
        <Field label="最大输出 Token">
          <input disabled={busy} type="number" min={1} max={1024} value={maxTokens} onChange={e => setMaxTokens(+e.target.value)} />
        </Field>
        <div className="checks">
          <label>
            <input
              type="checkbox"
              checked={includeContent}
              disabled={busy || !canPreviewContent}
              onChange={e => setIncludeContent(e.target.checked)}
            />
            显示回答内容预览（仅超级管理员；不持久化）
          </label>
        </div>
        {!canPreviewContent && <p className="hint">默认结果只包含状态、耗时、模型与 Token；回答内容预览需要超级管理员权限。</p>}
      </div>

      {error && <div className="notice warning" role="alert"><b>诊断失败</b><p>{error}</p><p className="hint">修改测试参数后可重新诊断；失败不表示渠道已停用。</p></div>}

      <div className="formActions">
        <Button variant="primary" loading={busy} disabled={!upstream.trim() || !prompt.trim()} onClick={() => void run()}>
          开始诊断
        </Button>
      </div>

      {result && (
        <>
          <div className="secretBox success">
            <FlaskConical aria-hidden />
            <h3>诊断成功</h3>
            <p className="responseText">
              {result.status_code} · {ms(result.latency_ms)} · {result.total_tokens} tokens
            </p>
          </div>
          <Detail
            title="调用指标"
            rows={[
              ['渠道', result.channel],
              ['上游模型', result.upstream_model],
              ['HTTP 状态', result.status_code],
              ['延迟', ms(result.latency_ms)],
              ['Input Tokens', result.input_tokens],
              ['Output Tokens', result.output_tokens],
              ['Total Tokens', result.total_tokens],
            ]}
          />
          {result.content ? (
            <div className="notice info">
              <b>回答内容预览（未持久化）</b>
              <p className="responseText">{result.content}</p>
            </div>
          ) : null}
        </>
      )}
    </div>
  )
}
