import {useCallback, useMemo, useState} from 'react'
import {ChevronLeft, ChevronRight, Network, Pencil, Plus, RefreshCw, Trash2} from 'lucide-react'
import {api, type CircuitView, type DashboardStatus, type ModelConfig, type ModelRoute, type Provider, type ProviderDeleteResult} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ProviderForm, type ProviderDraft} from '../components/providers/ProviderForm'
import {ProviderTestPanel} from '../components/providers/ProviderTestPanel'
import {ProviderModelsPanel} from '../components/providers/ProviderModelsPanel'
import {friendlyProviderName, PROTOCOLS, protocolLabel} from '../components/providers/templates'
import {useResource} from '../lib/useResource'
import {ms, when} from '../lib/format'

const PAGE_SIZE = 20

type Filters = {name: string; provider_type: string; protocol: string; status: string}
const emptyFilters: Filters = {name: '', provider_type: '', protocol: '', status: ''}

type Tab = 'basic' | 'models' | 'test' | 'usage'

type Bundle = {
  providers: Provider[]
  total: number
  circuits: CircuitView[]
  sampledAt: Date
}

/**
 * deleteCost is the price the confirmation quotes. It is a preview computed
 * from the same route endpoints the model page uses; the DELETE response is the
 * actual result and the UI shows both.
 */
async function deleteCost(providerID: string) {
  const models = (await api<{data: ModelConfig[]}>('/admin/models')).data || []
  const lists = await Promise.all(
    models.map(async m => (await api<{data: ModelRoute[]}>(`/admin/models/${m.id}/routes`)).data || []),
  )
  let routes = 0
  let modelsLost = 0
  for (const list of lists) {
    const own = list.filter(r => r.provider_id === providerID).length
    routes += own
    if (own > 0 && own === list.length) modelsLost++
  }
  return {routes, modelsLost}
}

export function ProvidersPage({canPreviewContent = false}: {canPreviewContent?: boolean}) {
  const toast = useToast()
  const confirm = useConfirm()
  const [draftFilters, setDraftFilters] = useState<Filters>(emptyFilters)
  const [filters, setFilters] = useState<Filters>(emptyFilters)
  const [offset, setOffset] = useState(0)
  const [draft, setDraft] = useState<ProviderDraft | null>(null)
  const [tab, setTab] = useState<Tab>('basic')
  const [deleting, setDeleting] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batching, setBatching] = useState(false)

  const query = useMemo(() => {
    const q = new URLSearchParams()
    if (filters.name.trim()) q.set('name', filters.name.trim())
    if (filters.provider_type.trim()) q.set('provider_type', filters.provider_type.trim())
    if (filters.protocol) q.set('protocol', filters.protocol)
    if (filters.status) q.set('status', filters.status)
    q.set('limit', String(PAGE_SIZE))
    q.set('offset', String(offset))
    return q.toString()
  }, [filters, offset])

  const loader = useCallback(async (): Promise<Bundle> => {
    const [p, status] = await Promise.all([
      api<{data: Provider[]; total?: number}>(`/admin/providers?${query}`),
      api<DashboardStatus>('/admin/status').catch(() => null),
    ])
    return {
      providers: p.data || [],
      total: p.total ?? (p.data || []).length,
      circuits: status?.circuits || [],
      sampledAt: new Date(),
    }
  }, [query])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [query])

  const save = async (v: ProviderDraft) => {
    try {
      await api(v.id ? `/admin/providers/${v.id}` : '/admin/providers', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success('渠道已保存')
      setDraft(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存渠道失败')
    }
  }

  const remove = async (p: Provider) => {
    let cost: {routes: number; modelsLost: number}
    try {
      cost = await deleteCost(p.id)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法读取该渠道的删除影响')
      return
    }
    const ok = await confirm({
      title: `删除渠道「${p.name}」？`,
      description: `将永久删除 1 个渠道、${cost.routes} 条线路、${cost.modelsLost} 个对外模型。此操作不可撤销。`,
      confirmText: '永久删除',
      danger: true,
      requireText: p.name,
      requireTextLabel: `输入渠道名「${p.name}」以确认`,
    })
    if (!ok) return
    setDeleting(p.id)
    try {
      const result = await api<ProviderDeleteResult>(`/admin/providers/${encodeURIComponent(p.id)}`, {method: 'DELETE'})
      toast.success(
        `已删除渠道：${result.deleted_routes} 条线路、${result.deleted_virtual_models} 个对外模型、${result.deleted_provider_models} 条模型清单`,
      )
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除渠道失败')
    } finally {
      setDeleting(null)
    }
  }

  const toggleSelected = (id: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else if (next.size < 50) next.add(id)
      return next
    })
  }

  const batchStatus = async (status: 1 | 2) => {
    if (!selected.size) return
    setBatching(true)
    const results: {name: string; ok: boolean}[] = []
    for (const p of data?.providers || []) {
      if (!selected.has(p.id)) continue
      try {
        await api(`/admin/providers/${p.id}`, {method: 'PATCH', body: JSON.stringify({status})})
        results.push({name: p.name, ok: true})
      } catch {
        results.push({name: p.name, ok: false})
      }
    }
    setBatching(false)
    const failed = results.filter(r => !r.ok)
    if (failed.length) {
      toast.error(`${results.length - failed.length} 个成功，${failed.length} 个失败：${failed.map(f => f.name).join('、')}`)
    } else {
      toast.success(`${results.length} 个渠道已${status === 1 ? '启用' : '停用'}`)
    }
    setSelected(new Set())
    reload()
  }

  const items = data?.providers || []
  const total = data?.total || 0
  const circuits = new Map((data?.circuits || []).map(c => [c.provider, c]))
  const circuitLabel = (name: string) => {
    const c = circuits.get(name)
    if (!c) return '无近期尝试'
    const retry = c.retry_in_ms > 0 ? `，${Math.ceil(c.retry_in_ms / 1000)}s 后探测` : ''
    return `${c.state}${retry}`
  }

  return (
    <>
      <Panel
        title="上游渠道"
        sub="配置模型渠道的接入地址与凭据来源。凭据加密保存、只写不读；候选模型与诊断测试在渠道详情中完成。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => { setTab('basic'); setDraft({status: 1, protocol: 'openai-chat'}) }}>
            新增渠道
          </Button>
        }
      >
        <div className="filters">
          <label className="filterField">
            <span>名称</span>
            <input value={draftFilters.name} onChange={e => setDraftFilters({...draftFilters, name: e.target.value})} />
          </label>
          <label className="filterField">
            <span>供应商类型</span>
            <input value={draftFilters.provider_type} onChange={e => setDraftFilters({...draftFilters, provider_type: e.target.value})} placeholder="qwen / deepseek" />
          </label>
          <label className="filterField">
            <span>协议</span>
            <select value={draftFilters.protocol} onChange={e => setDraftFilters({...draftFilters, protocol: e.target.value})}>
              <option value="">全部</option>
              {PROTOCOLS.map(p => <option key={p} value={p}>{protocolLabel(p)}</option>)}
            </select>
          </label>
          <label className="filterField">
            <span>状态</span>
            <select value={draftFilters.status} onChange={e => setDraftFilters({...draftFilters, status: e.target.value})}>
              <option value="">全部</option>
              <option value="1">启用</option>
              <option value="2">停用</option>
            </select>
          </label>
          <div className="filterActions">
            <Button onClick={() => { setFilters(emptyFilters); setDraftFilters(emptyFilters); setOffset(0) }}>重置</Button>
            <Button variant="primary" onClick={() => { setFilters(draftFilters); setOffset(0) }}>筛选</Button>
          </div>
        </div>

        {selected.size > 0 && (
          <div className="notice info">
            已选择 {selected.size} 个渠道（最多 50 个）。
            <Button size="sm" loading={batching} onClick={() => void batchStatus(1)}>批量启用</Button>
            <Button size="sm" variant="danger" loading={batching} onClick={() => void batchStatus(2)}>批量停用</Button>
          </div>
        )}

        {loading && !data ? (
          <SkeletonCards count={3} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : items.length ? (
          <div className="providerCards">
            {items.map(p => {
              const friendly = friendlyProviderName(p.name)
              return (
                <article className={`providerCard ${p.status === 1 ? '' : 'disabled'}`} key={p.id}>
                  <header className="providerHead">
                    <div className="providerIdentity">
                      <input
                        type="checkbox"
                        aria-label={`选择渠道 ${p.name}`}
                        checked={selected.has(p.id)}
                        onChange={() => toggleSelected(p.id)}
                      />
                      {friendly && <span className="providerFriendly">{friendly}</span>}
                      <h3 className="providerName">{p.name}</h3>
                    </div>
                    <StatusBadge status={p.status} />
                  </header>

                  <dl className="providerRows">
                    <div className="providerRow">
                      <dt>供应商类型</dt>
                      <dd>{p.provider_type || '未设置'}</dd>
                    </div>
                    <div className="providerRow">
                      <dt>接口协议</dt>
                      <dd>{protocolLabel(p.protocol)}</dd>
                    </div>
                    <div className="providerRow">
                      <dt>Base URL</dt>
                      <dd className="mono cut" title={p.base_url}>{p.base_url || '—'}</dd>
                    </div>
                    <div className="providerRow">
                      <dt>凭据</dt>
                      <dd className="mono" title={p.credential_env || undefined}>
                        {p.credential?.source_type === 'env'
                          ? `${p.credential_env || '—'}${p.credential.configured ? '' : '（未读取到值）'}`
                          : p.credential?.source_type === 'inline'
                            ? `内联密钥${p.credential.last4 ? ` · 尾号 ${p.credential.last4}` : ''}`
                            : '—'}
                      </dd>
                    </div>
                    <div className="providerRow">
                      <dt>候选 / 引用</dt>
                      <dd>{p.candidate_model_count ?? 0} 个候选 · {p.referenced_route_count ?? 0} 条线路 · {p.referenced_model_count ?? 0} 个模型</dd>
                    </div>
                    <div className="providerRow">
                      <dt>最近测试</dt>
                      <dd>
                        {p.last_test
                          ? `${p.last_test.ok ? '成功' : '失败'} · HTTP ${p.last_test.status_code} · ${ms(p.last_test.latency_ms)} · ${when(p.last_test.at)}`
                          : '未测试'}
                      </dd>
                    </div>
                    <div className="providerRow">
                      <dt>当前实例熔断</dt>
                      <dd>{circuitLabel(p.name)} <span className="muted">（本进程，采样于 {data ? data.sampledAt.toLocaleTimeString() : '—'}）</span></dd>
                    </div>
                  </dl>

                  <footer className="providerFoot">
                    <Button size="sm" icon={<Pencil />} onClick={() => { setTab('basic'); setDraft(p) }}>打开</Button>
                    <Button size="sm" icon={<RefreshCw />} onClick={() => { setTab('test'); setDraft(p) }}>诊断测试</Button>
                    <Button
                      size="sm"
                      variant="danger"
                      icon={<Trash2 />}
                      loading={deleting === p.id}
                      disabled={deleting !== null}
                      onClick={() => void remove(p)}
                    >
                      删除
                    </Button>
                  </footer>
                </article>
              )
            })}
          </div>
        ) : (
          <EmptyState
            icon={<Network />}
            title="暂无渠道"
            description="添加渠道后即可维护候选模型并配置线路。"
            action={<Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1, protocol: 'openai-chat'})}>新增渠道</Button>}
          />
        )}

        <div className="pager">
          <span className="muted">第 {offset + 1}–{offset + items.length} 条，共 {total} 条</span>
          <div className="pagerActions">
            <Button size="sm" icon={<ChevronLeft />} disabled={offset === 0 || loading} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>上一页</Button>
            <Button size="sm" disabled={offset + PAGE_SIZE >= total || loading} onClick={() => setOffset(offset + PAGE_SIZE)}>下一页 <ChevronRight aria-hidden /></Button>
          </div>
        </div>
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? `渠道：${draft.name}` : '新增渠道'}
          subtitle="密钥加密保存、只写不读；也可以改为引用环境变量。"
          onClose={() => setDraft(null)}
        >
          <div className="drawerStack">
            {draft.id && (
              <div className="segmented" role="group" aria-label="渠道详情">
                {(['basic', 'models', 'test', 'usage'] as Tab[]).map(t => (
                  <button
                    key={t}
                    type="button"
                    className={`segmentedItem ${tab === t ? 'on' : ''}`}
                    aria-pressed={tab === t}
                    onClick={() => setTab(t)}
                  >
                    {t === 'basic' ? '基本配置' : t === 'models' ? '模型列表' : t === 'test' ? '调用测试' : '使用记录'}
                  </button>
                ))}
              </div>
            )}
            {tab === 'basic' && <ProviderForm value={draft} onSave={v => void save(v)} onCancel={() => setDraft(null)} />}
            {tab === 'models' && draft.id && (
              <ProviderModelsPanel providerID={draft.id} onChanged={reload} />
            )}
            {tab === 'test' && draft.id && (
              <ProviderTestPanel provider={draft as Provider} canPreviewContent={canPreviewContent} onTested={reload} />
            )}
            {tab === 'usage' && (
              <p className="hint">
                业务调用日志仅超级管理员与审计员可读。gateway_admin 的渠道范围运行摘要将在运行摘要票中接入。
              </p>
            )}
          </div>
        </Drawer>
      )}
    </>
  )
}
