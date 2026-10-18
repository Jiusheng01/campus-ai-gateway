import {useCallback, useState} from 'react'
import {ChevronDown, FlaskConical, Pencil, Plus, RefreshCw, Route, Trash2} from 'lucide-react'
import {api, type DashboardStatus, type ModelConfig, type ModelRoute, type Provider} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ModelForm, type ModelDraft} from '../components/models/ModelForm'
import {RouteForm, type RouteDraft} from '../components/models/RouteForm'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {friendlyProviderName} from '../components/providers/templates'
import {useResource} from '../lib/useResource'

type ModelBundle = {models: ModelConfig[]; providers: Provider[]; routes: Record<string, ModelRoute[]>; dataPlane: 'direct' | 'thinkwatch'}

type DrawerState =
  | {kind: 'model'; draft: ModelDraft}
  | {kind: 'route'; model: ModelConfig; draft: RouteDraft}
  | {kind: 'test'; model?: string}
  | null

export function ModelsPage() {
  const toast = useToast()
  const confirm = useConfirm()
  const [drawer, setDrawer] = useState<DrawerState>(null)
  const [syncing, setSyncing] = useState(false)

  const loader = useCallback(async (): Promise<ModelBundle> => {
    const [m, p, status] = await Promise.all([
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: Provider[]}>('/admin/providers'),
      // Which data plane is running decides whether 同步 ThinkWatch has
      // anything to write to. /admin/status is the only endpoint that reports
      // it, and every role that can open this page may read it.
      //
      // A failure here must not blank the page: the sync button is a
      // convenience, the model list is the point. Falling back to "thinkwatch"
      // matches the gateway's own default, so the worst case is a button that
      // answers with a clear 409 instead of disappearing.
      api<DashboardStatus>('/admin/status').catch(() => null),
    ])
    const models = m.data || []
    const routePairs = await Promise.all(
      models.map(async x => {
        const r = await api<{data: ModelRoute[]}>(`/admin/models/${x.id}/routes`)
        return [x.id, r.data || []] as const
      }),
    )
    return {
      models,
      providers: p.data || [],
      routes: Object.fromEntries(routePairs),
      dataPlane: status?.data_plane === 'direct' ? 'direct' : 'thinkwatch',
    }
  }, [])

  const {data, loading, error, errorStatus, reload} = useResource(loader)

  const sync = async () => {
    const ok = await confirm({
      title: '同步 ThinkWatch 配置？',
      description: '将当前渠道、模型和线路配置写入 ThinkWatch。同步后业务请求会立即按新线路路由。',
      confirmText: '确认同步',
    })
    if (!ok) return
    setSyncing(true)
    try {
      const out = await api<{providers: number; aliases: number}>('/admin/thinkwatch/sync', {method: 'POST'})
      toast.success(`ThinkWatch 同步成功：${out.providers} 个渠道 / ${out.aliases} 个对外模型`)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'ThinkWatch 同步失败')
    } finally {
      setSyncing(false)
    }
  }

  const saveModel = async (v: ModelDraft) => {
    try {
      await api(v.id ? `/admin/models/${v.id}` : '/admin/models', {method: v.id ? 'PUT' : 'POST', body: JSON.stringify(v)})
      toast.success('模型已保存')
      setDrawer(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存模型失败')
    }
  }

  const saveRoute = async (model: ModelConfig, v: RouteDraft) => {
    try {
      await api(v.id ? `/admin/model-routes/${v.id}` : `/admin/models/${model.id}/routes`, {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success('线路已保存')
      setDrawer(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存线路失败')
    }
  }

  const removeRoute = async (r: ModelRoute) => {
    const ok = await confirm({
      title: '删除备用线路？',
      description: `${r.provider_name} → ${r.upstream_model}。删除后该线路不再参与故障切换。`,
      confirmText: '删除线路',
      danger: true,
    })
    if (!ok) return
    try {
      await api(`/admin/model-routes/${r.id}`, {method: 'DELETE'})
      toast.success('线路已删除')
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除线路失败')
    }
  }

  const models = data?.models || []
  const providers = data?.providers || []
  const direct = data?.dataPlane === 'direct'

  return (
    <div className="stack">
      <div className="quickActions">
        {!direct && (
          <Button icon={<RefreshCw />} loading={syncing} onClick={() => void sync()}>
            同步 ThinkWatch
          </Button>
        )}
        <Button variant="primary" icon={<FlaskConical />} onClick={() => setDrawer({kind: 'test'})}>
          模型连通测试
        </Button>
      </div>

      <Panel
        title="模型与路由"
        sub="业务系统只使用统一的对外模型名称，实际渠道和上游模型可在这里配置并进行故障切换。"
        actions={
          <Button
            variant="primary"
            icon={<Plus />}
            disabled={!providers.length}
            onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}
          >
            新增模型
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonCards count={2} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : models.length ? (
          <div className="modelCards">
            {models.map(m => {
              const rs = (data?.routes[m.id] || []).slice().sort((a, b) => a.priority - b.priority || b.weight - a.weight)
              const primaryIdx = rs.findIndex(r => r.status === 1)
              return (
                <article className={`modelCard ${m.status === 1 ? '' : 'disabled'}`} key={m.id}>
                  <header className="modelHead">
                    <div className="modelIdentity">
                      <div className="modelAliasRow">
                        <h3 className="modelAlias">{m.alias}</h3>
                        <StatusBadge status={m.status} />
                      </div>
                      <span className="modelKind">对外模型</span>
                      <p className="modelHint">
                        业务系统使用 <code className="mono">model={m.alias}</code>
                      </p>
                    </div>
                    <div className="modelActions">
                      <Button size="sm" icon={<FlaskConical />} onClick={() => setDrawer({kind: 'test', model: m.alias})}>
                        模型测试
                      </Button>
                      <Button size="sm" icon={<Pencil />} onClick={() => setDrawer({kind: 'model', draft: m})}>
                        编辑模型
                      </Button>
                    </div>
                  </header>

                  <div className="routeChain">
                    {rs.map((r, i) => {
                      const isPrimary = i === primaryIdx
                      const friendly = friendlyProviderName(r.provider_name)
                      return (
                        <div className="routeChainItem" key={r.id}>
                          {i > 0 && (
                            <div className="routeConnector" aria-hidden>
                              <ChevronDown />
                              <span>失败自动切换</span>
                            </div>
                          )}
                          <div className={`routeStep ${r.status === 1 ? '' : 'isDisabled'} ${isPrimary ? 'isPrimary' : ''}`}>
                            <span className="routeIndex" aria-hidden>
                              {i + 1}
                            </span>
                            <div className="routeBox">
                              <div className="routeBoxHead">
                                <b>{friendly || r.provider_name}</b>
                                <span className="routeTags">
                                  {isPrimary ? <Badge tone="info">主线路</Badge> : <Badge>备用线路</Badge>}
                                  <StatusBadge status={r.status} />
                                </span>
                              </div>
                              {friendly && <span className="routeProvider mono">{r.provider_name}</span>}
                              <code className="routeModel mono">{r.upstream_model}</code>
                              <div className="routeMeta">
                                <span>
                                  优先级 <b>{r.priority}</b>
                                </span>
                                <span>
                                  流量权重 <b>{r.weight}</b>
                                </span>
                                <span>
                                  超时 <b>{r.timeout_ms} ms</b>
                                </span>
                              </div>
                              <div className="routeActions">
                                <button
                                  className="link"
                                  onClick={() =>
                                    setDrawer({
                                      kind: 'route',
                                      model: m,
                                      draft: r,
                                    })
                                  }
                                >
                                  编辑
                                </button>
                                <button className="link dangerText" onClick={() => void removeRoute(r)}>
                                  <Trash2 aria-hidden />
                                  删除
                                </button>
                              </div>
                            </div>
                          </div>
                        </div>
                      )
                    })}
                    {!rs.length && (
                      <EmptyState title="该模型尚未配置线路" description="添加一条线路后，业务请求才能被路由到渠道。" />
                    )}
                  </div>

                  <button
                    className="addRoute"
                    onClick={() =>
                      setDrawer({
                        kind: 'route',
                        model: m,
                        draft: {priority: rs.length + 1, weight: 100, timeout_ms: 60000, status: 1},
                      })
                    }
                  >
                    <Plus aria-hidden />
                    添加备用线路
                  </button>
                </article>
              )
            })}
          </div>
        ) : (
          <EmptyState
            icon={<Route />}
            title="暂无对外模型"
            description={providers.length ? '创建第一个对外模型，业务系统即可开始调用。' : '请先添加渠道，再创建对外模型。'}
            action={
              providers.length ? (
                <Button variant="primary" icon={<Plus />} onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}>
                  新增模型
                </Button>
              ) : undefined
            }
          />
        )}
      </Panel>

      {drawer?.kind === 'model' && (
        <Drawer
          title={drawer.draft.id ? '编辑对外模型' : '新增对外模型'}
          subtitle="对外模型名称是业务系统的唯一入口，上游模型由线路决定。"
          onClose={() => setDrawer(null)}
        >
          <ModelForm value={drawer.draft} providers={providers} onSave={v => void saveModel(v)} onCancel={() => setDrawer(null)} />
        </Drawer>
      )}

      {drawer?.kind === 'route' && (
        <Drawer
          title={drawer.draft.id ? '编辑线路' : '新增备用线路'}
          subtitle={`对外模型 ${drawer.model.alias}`}
          onClose={() => setDrawer(null)}
        >
          <RouteForm
            value={drawer.draft}
            model={drawer.model}
            providers={providers}
            onSave={(m, v) => void saveRoute(m, v)}
            onCancel={() => setDrawer(null)}
          />
        </Drawer>
      )}

      {drawer?.kind === 'test' && <ModelTestDrawer models={models} initialModel={drawer.model} direct={direct} onClose={() => setDrawer(null)} />}
    </div>
  )
}
