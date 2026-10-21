import {useCallback, useMemo, useState} from 'react'
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronUp,
  Clock,
  Layers,
  Network,
  Plus,
  Route,
  Trash2,
  Zap,
} from 'lucide-react'
import {api, type ModelDetail, type ModelRoute, type Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {SkeletonRows} from '../ui/Skeleton'
import {useToast} from '../ui/Toast'
import {useResource} from '../../lib/useResource'
import {
  diffRoutes,
  projectMode,
  weightedShares,
  type RouteDraft,
  type RouteMode,
} from './routeModes'

function toDraft(routes: ModelRoute[]): RouteDraft[] {
  return routes.map(r => ({
    id: r.id,
    provider_id: r.provider_id,
    upstream_model: r.upstream_model,
    priority: r.priority,
    weight: r.weight,
    timeout_ms: r.timeout_ms,
    status: r.status,
  }))
}

const modeLabel: Record<RouteMode, string> = {
  primary: '主备容灾',
  weighted: '加权分流',
  advanced: '高级',
}

export function RouteEditor({modelID, onSaved}: {modelID: string; onSaved?: () => void}) {
  const toast = useToast()
  const loader = useCallback(async () => {
    const [detail, providers] = await Promise.all([
      api<ModelDetail>(`/admin/models/${modelID}`),
      api<{data: Provider[]}>('/admin/providers').then(d => d.data || []),
    ])
    return {detail, providers}
  }, [modelID])
  const {data, loading, error, errorStatus, reload} = useResource(loader, [modelID])
  const [draft, setDraft] = useState<RouteDraft[] | null>(null)
  const [mode, setMode] = useState<RouteMode>('advanced')
  const [pending, setPending] = useState<{mode: RouteMode; projected: RouteDraft[]; diff: string[]} | null>(null)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<number | null>(null)
  const [formError, setFormError] = useState('')

  const routes = useMemo(() => draft ?? (data ? toDraft(data.detail.routes) : []), [draft, data])
  const version = data?.detail.version ?? 0
  const providers = data?.providers || []
  const shares = weightedShares(routes)

  const setRow = (i: number, patch: Partial<RouteDraft>) => {
    setDraft(prev => {
      const next = (prev ?? (data ? toDraft(data.detail.routes) : [])).slice()
      next[i] = {...next[i], ...patch}
      return next
    })
  }

  const addRow = () => {
    const maxPriority = routes.reduce((m, r) => Math.max(m, r.priority), 0)
    const next = (draft ?? (data ? toDraft(data.detail.routes) : [])).slice()
    next.push({
      provider_id: providers[0]?.id || '',
      upstream_model: '',
      priority: maxPriority + 1,
      weight: 100,
      timeout_ms: 60000,
      status: 1,
    })
    setDraft(next)
  }

  const removeRow = (i: number) => {
    const next = (draft ?? (data ? toDraft(data.detail.routes) : [])).slice()
    next.splice(i, 1)
    setDraft(next)
  }

  const moveRow = (i: number, delta: number) => {
    const next = (draft ?? (data ? toDraft(data.detail.routes) : [])).slice()
    const j = i + delta
    if (j < 0 || j >= next.length) return
    ;[next[i], next[j]] = [next[j], next[i]]
    if (mode === 'primary') next.forEach((r, idx) => { r.priority = idx + 1 })
    setDraft(next)
  }

  const switchMode = (next: RouteMode) => {
    if (next === mode) return
    const projected = projectMode(next, routes)
    const diff = diffRoutes(routes, projected)
    if (diff.length) {
      setPending({mode: next, projected, diff})
      return
    }
    setMode(next)
    setDraft(projected)
    setPending(null)
  }

  const confirmMode = () => {
    if (!pending) return
    setDraft(pending.projected)
    setMode(pending.mode)
    setPending(null)
  }

  const save = async () => {
    setBusy(true)
    setFormError('')
    setConflict(null)
    try {
      const out = await api<{version: number; routes: ModelRoute[]}>(
        `/admin/models/${modelID}/routes`,
        {
          method: 'PUT',
          body: JSON.stringify({expected_version: version, routes}),
        },
      )
      toast.success(`线路已保存，配置版本 ${out.version}`)
      setDraft(null)
      reload()
      onSaved?.()
    } catch (e) {
      const apiError = e as {status?: number; message?: string; code?: string}
      if (apiError.status === 409) {
        const match = /current[_ ]version[^0-9]*(\d+)/i.exec(apiError.message || '')
        setConflict(match ? Number(match[1]) : null)
      } else {
        setFormError(apiError.message || '保存线路失败')
      }
    } finally {
      setBusy(false)
    }
  }

  if (loading && !data) return <SkeletonRows rows={4} columns={7} />
  if (error) return <ErrorState message={error} status={errorStatus} onRetry={reload} />
  if (!data) return null

  return (
    <div className="drawerStack">
      {/* Route mode segmented buttons */}
      <div className="segmented" role="group" aria-label="路由编辑模式">
        {(['primary', 'weighted', 'advanced'] as RouteMode[]).map(m => (
          <button
            key={m}
            type="button"
            className={`segmentedItem ${mode === m ? 'on' : ''}`}
            aria-pressed={mode === m}
            onClick={() => switchMode(m)}
          >
            {modeLabel[m]}
          </button>
        ))}
      </div>

      <Notice>
        优先级数字越小越先尝试；同一优先级内按权重做平滑轮询。超时是上游响应头等待时间，不是完整回答时间。
      </Notice>

      {/* Mode Switch Confirmation Notice with Exact Diff */}
      {pending && (
        <div className="notice warning" role="alert">
          <AlertTriangle aria-hidden />
          <div className="noticeTextContent">
            <b>切换到「{modeLabel[pending.mode]}」会改变以下字段，确认后只修改草稿，尚未保存：</b>
            <ul className="drawerSectionList">
              {pending.diff.map(line => (
                <li key={line}><code>{line}</code></li>
              ))}
            </ul>
            <div className="formActions">
              <Button size="sm" onClick={() => setPending(null)}>取消切换</Button>
              <Button size="sm" variant="primary" onClick={confirmMode}>确认并应用</Button>
            </div>
          </div>
        </div>
      )}

      {conflict !== null && (
        <div className="notice warning" role="alert">
          <AlertTriangle aria-hidden />
          <div>
            <p>该模型的线路已被其他管理员修改。你的草稿仍保留；当前版本为 {conflict}。</p>
            <Button size="sm" onClick={() => { setDraft(null); setConflict(null); reload() }}>
              加载最新版本
            </Button>
          </div>
        </div>
      )}

      {formError && <div className="notice warning" role="alert">{formError}</div>}

      {/* Routes List */}
      {routes.length ? (
        <div className="routeEditorCards">
          {routes.map((r, i) => {
            const isFirst = i === 0
            const isLast = i === routes.length - 1
            const providerName = providers.find(p => p.id === r.provider_id)?.name || '未选择渠道'

            return (
              <div className={`routeRuleCard ${mode}`} key={r.id || `new-${i}`}>
                <div className="routeRuleCardHead">
                  <div className="routeRuleCardTitleRow">
                    <span className="routeRuleIndex">#{i + 1}</span>
                    <b className="routeRuleChannelName">{providerName}</b>
                    <span className="routeRuleModelTag mono">{r.upstream_model || '（未输入上游模型）'}</span>
                    <span className={`statusChip ${r.status === 1 ? 'ok' : 'down'}`}>
                      <i className={`dot ${r.status === 1 ? 'on' : 'down'}`} aria-hidden />
                      {r.status === 1 ? '启用' : '停用'}
                    </span>
                  </div>

                  <div className="routeRuleCardActions">
                    {mode === 'primary' && (
                      <div className="routeOrderControls">
                        <Button
                          size="sm"
                          icon={<ArrowUp />}
                          disabled={isFirst}
                          onClick={() => moveRow(i, -1)}
                          title="上移优先级"
                        >
                          上移
                        </Button>
                        <Button
                          size="sm"
                          icon={<ArrowDown />}
                          disabled={isLast}
                          onClick={() => moveRow(i, 1)}
                          title="下移优先级"
                        >
                          下移
                        </Button>
                      </div>
                    )}
                    <Button
                      size="sm"
                      variant="danger"
                      icon={<Trash2 />}
                      onClick={() => removeRow(i)}
                      title="删除该线路"
                    >
                      删除
                    </Button>
                  </div>
                </div>

                <div className="routeRuleCardFields">
                  <Field label="上游渠道" className="routeField">
                    <select
                      value={r.provider_id}
                      onChange={e => setRow(i, {provider_id: e.target.value})}
                    >
                      {providers.map(p => (
                        <option key={p.id} value={p.id}>
                          {p.name} {p.provider_type ? `(${p.provider_type})` : ''}
                        </option>
                      ))}
                    </select>
                  </Field>

                  <Field label="上游模型名称" className="routeField">
                    <input
                      className="mono"
                      placeholder="例如：deepseek-chat"
                      value={r.upstream_model}
                      onChange={e => setRow(i, {upstream_model: e.target.value})}
                    />
                  </Field>

                  {mode !== 'primary' && (
                    <>
                      <Field label={mode === 'weighted' ? '层（优先级）' : '优先级'} className="routeField">
                        <input
                          type="number"
                          min={1}
                          max={1000}
                          value={r.priority}
                          onChange={e => setRow(i, {priority: +e.target.value})}
                        />
                      </Field>

                      <Field
                        label="权重"
                        hint={r.weight === 0 ? '兼容值，按 1 调度' : undefined}
                        className="routeField"
                      >
                        <input
                          type="number"
                          min={0}
                          max={10000}
                          value={r.weight}
                          onChange={e => setRow(i, {weight: +e.target.value})}
                        />
                      </Field>
                    </>
                  )}

                  <Field label="超时等待（ms）" className="routeField">
                    <input
                      type="number"
                      min={1}
                      max={600000}
                      step={1000}
                      value={r.timeout_ms}
                      onChange={e => setRow(i, {timeout_ms: +e.target.value})}
                    />
                  </Field>

                  <Field label="状态" className="routeField">
                    <select
                      value={r.status}
                      onChange={e => setRow(i, {status: +e.target.value})}
                    >
                      <option value="1">启用</option>
                      <option value="2">停用</option>
                    </select>
                  </Field>
                </div>
              </div>
            )
          })}
        </div>
      ) : (
        <EmptyState
          title="没有线路"
          description="至少保留一条线路，已启用的模型至少保留一条启用线路。"
        />
      )}

      {mode === 'primary' && (
        <div className="cardNotice">
          <b>主备容灾规则：</b>
          <span>主备顺序即优先级：第 1 条为 priority 1，失败后进入下一条。可使用上方按钮调整顺序。</span>
        </div>
      )}

      {mode === 'weighted' && (
        <div className="notice info">
          <b>目标流量比例（仅配置目标，不代表实际成功率）：</b>
          <div className="sharesList">
            {shares.map(s => (
              <div key={s.priority} className="sharePriorityGroup">
                <span className="shareGroupTitle">第 {s.priority} 优先级层：</span>
                <div className="shareBadges">
                  {s.items.map((item, idx) => (
                    <span key={idx} className="shareBadge">
                      <b>{item.route.upstream_model || item.route.provider_id.slice(0, 6)}</b>
                      <span className="sharePct">{item.pct.toFixed(1)}%</span>
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <details className="formAdvanced">
        <summary>查看将要保存的线路数组 (JSON)</summary>
        <pre className="mono responseText">{JSON.stringify(routes, null, 2)}</pre>
      </details>

      <div className="formActions">
        <Button icon={<Plus />} onClick={addRow}>
          添加线路
        </Button>
        <Button
          variant="primary"
          loading={busy}
          disabled={busy || !routes.length || !!pending}
          onClick={() => void save()}
        >
          保存整组线路
        </Button>
      </div>
      <p className="hint">保存会原子替换整组线路，并保留未修改线路的 route ID。当前配置版本 {version}。</p>
    </div>
  )
}
