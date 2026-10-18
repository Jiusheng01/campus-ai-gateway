import {useState} from 'react'
import {FlaskConical, Route} from 'lucide-react'
import {api, type ModelTestOption} from '../../api'
import {Drawer} from '../ui/Drawer'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {Detail} from '../ui/Detail'
import {useToast} from '../ui/Toast'
import {ms} from '../../lib/format'

type TestResult = {
  content?: string
  model?: string
  /** Which channel answered. Only reported by the direct data plane. */
  channel?: string
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  latency_ms?: number
}

/**
 * 模型连通测试.
 *
 * This calls the admin API, which reaches the provider along whichever data
 * plane the gateway is running: through ThinkWatch, or — when ThinkWatch is not
 * configured — straight to the channel. Either way it is NOT a business
 * data-plane request: no virtual API key, no tenant quota, and nothing lands in
 * gateway_request_log. The copy says so explicitly, because an operator
 * debugging "why is production failing" would otherwise read a green result as
 * proof the business path works.
 */
export function ModelTestDrawer({
  models,
  initialModel,
  direct = false,
  onClose,
}: {
  models: ModelTestOption[]
  initialModel?: string
  /** Whether the gateway is running the direct data plane. */
  direct?: boolean
  onClose: () => void
}) {
  const enabled = models.filter(m => m.status === 1)
  const [model, setModel] = useState(initialModel || enabled[0]?.alias || '')
  const [prompt, setPrompt] = useState('请只回复：MODEL_OK')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<TestResult | null>(null)
  const toast = useToast()

  const run = async () => {
    if (!model || !prompt.trim()) return
    setBusy(true)
    setResult(null)
    try {
      const out = await api<TestResult>('/admin/model-test', {
        method: 'POST',
        body: JSON.stringify({model, prompt: prompt.trim()}),
      })
      setResult(out)
      toast.success('模型连通测试成功')
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '模型连通测试失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Drawer
      title="模型连通测试"
      subtitle="验证对外模型的线路是否可以正常返回"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>关闭</Button>
          <Button variant="primary" loading={busy} disabled={!model || !prompt.trim()} icon={<FlaskConical />} onClick={() => void run()}>
            开始测试
          </Button>
        </>
      }
    >
      <div className="drawerStack">
        <Notice>
          <Route aria-hidden />
          {direct ? (
            <>
              此测试由网关直接向渠道发起请求，<b>不会</b>经过业务 API 密钥、租户配额和使用日志。
            </>
          ) : (
            <>
              此测试通过 ThinkWatch 验证模型线路，<b>不会</b>经过业务 API 密钥、租户配额和使用日志。
            </>
          )}
        </Notice>

        <div className="form">
          <Field label="对外模型" hint="业务系统调用 /v1/* API 时使用的 model 名称。">
            <select value={model} onChange={e => setModel(e.target.value)}>
              {enabled.map(m => (
                <option key={m.id} value={m.alias}>
                  {m.alias}
                </option>
              ))}
            </select>
          </Field>
          <Field label="测试内容">
            <textarea rows={4} value={prompt} onChange={e => setPrompt(e.target.value)} />
          </Field>
        </div>

        {result && (
          <>
            <div className="secretBox success">
              <FlaskConical aria-hidden />
              <h3>测试成功</h3>
              <p className="responseText">{result.content || '(空响应)'}</p>
            </div>
            <Detail
              title="调用指标"
              rows={[
                ['模型', result.model || model],
                ...(result.channel ? ([['渠道', result.channel]] as [string, string][]) : []),
                ['Input Tokens', result.input_tokens ?? 0],
                ['Output Tokens', result.output_tokens ?? 0],
                ['Total Tokens', result.total_tokens ?? 0],
                ['延迟', ms(result.latency_ms || 0)],
              ]}
            />
            <p className="authHint">此结果不会出现在业务使用日志中。</p>
          </>
        )}
      </div>
    </Drawer>
  )
}
