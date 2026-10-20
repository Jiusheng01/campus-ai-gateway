import {useCallback, useState} from 'react'
import {ChevronDown, FlaskConical, Pencil, Plus, Route, Scale, Trash2} from 'lucide-react'
import {api, type ModelConfig, type ModelRoute, type Provider} from '../api'
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

type ModelBundle = {models: ModelConfig[]; providers: Provider[]; routes: Record<string, ModelRoute[]>}

type DrawerState =
  | {kind: 'model'; draft: ModelDraft}
  | {kind: 'route'; model: ModelConfig; draft: RouteDraft}
  | {kind: 'test'; model?: string}
  | null

type RouteGroup = {priority: number; routes: ModelRoute[]}

/**
 * groupRoutes collapses the flat route list into the priority bands the data
 * plane actually uses. Everything in one band is load-balanced; bands are tried
 * in ascending order, so the first band is the preferred one and the rest are
 * failover. Rendering them as a single numbered chain hid that distinction.
 */
function groupRoutes(routes: ModelRoute[]): RouteGroup[] {
  const sorted = routes.slice().sort((a, b) => a.priority - b.priority || b.weight - a.weight)
  const groups: RouteGroup[] = []
  for (const r of sorted) {
    const last = groups[groups.length - 1]
    if (last && last.priority === r.priority) last.routes.push(r)
    else groups.push({priority: r.priority, routes: [r]})
  }
  return groups
}

export function ModelsPage() {
  const toast = useToast()
  const confirm = useConfirm()
  const [drawer, setDrawer] = useState<DrawerState>(null)

  const loader = useCallback(async (): Promise<ModelBundle> => {
    const [m, p] = await Promise.all([
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: Provider[]}>('/admin/providers'),
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
    }
  }, [])

  const {data, loading, error, errorStatus, reload} = useResource(loader)

  const saveModel = async (v: ModelDraft) => {
    try {
      // The catalog write carries only the fields the form owns. The server
      // rejects an alias change on update rather than treating it as a rename.
      await api(v.id ? `/admin/models/${v.id}` : '/admin/models', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify({alias: v.alias, display_name: v.display_name, status: v.status}),
      })
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
      title: '删除线路？',
      description: `${r.provider_name} → ${r.upstream_model}。删除后该线路不再参与分流或故障切换。`,
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
  const canAddRoute = providers.length > 0

  const addRoute = (model: ModelConfig, routes: ModelRoute[]) => {
    // A new route starts as its own failover band. Sharing a band is a
    // deliberate choice — it changes how traffic is split — so it is not the
    // default the operator gets by accident.
    const nextPriority = routes.reduce((max, r) => Math.max(max, r.priority), 0) + 1
    setDrawer({kind: 'route', model, draft: {priority: nextPriority, weight: 100, timeout_ms: 60000, status: 1}})
  }

  return (
    <div className="stack">
      <div className="quickActions">
        <Button variant="primary" icon={<FlaskConical />} onClick={() => setDrawer({kind: 'test'})}>
          模型连通测试
        </Button>
      </div>

      <Panel
        title="模型与线路"
        sub="业务系统只使用统一的对外模型名称。同优先级的线路按权重分流，整组失败后自动切换到下一优先级。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}>
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
              const rs = data?.routes[m.id] || []
              const groups = groupRoutes(rs)
              let step = 0
              return (
                <article className={`modelCard ${m.status === 1 ? '' : 'disabled'}`} key={m.id}>
                  <header className="modelHead">
                    <div className="modelIdentity">
                      <div className="modelAliasRow">
                        <h3 className="modelAlias">{m.display_name || m.alias}</h3>
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
                    {groups.map((g, gi) => {
                      const failover = gi > 0
                      return (
                        <div className="routeGroup" key={g.priority}>
                          {failover && (
                            <div className="routeConnector" aria-hidden>
                              <ChevronDown />
                              <span>整组失败后切换</span>
                            </div>
                          )}
                          <div className="routeGroupHead">
                            <span className="routeGroupTitle">
                              优先级 {g.priority} · {failover ? '故障切换组' : '首选组'}
                            </span>
                            {g.routes.length > 1 ? (
                              <Badge tone="info">
                                <Scale aria-hidden />
                                组内加权分流
                              </Badge>
                            ) : (
                              <Badge>单线路</Badge>
                            )}
                          </div>
                          <div className="routeGroupBody">
                            {g.routes.map(r => {
                              step += 1
                              const friendly = friendlyProviderName(r.provider_name)
                              return (
                                <div className={`routeStep ${r.status === 1 ? '' : 'isDisabled'}`} key={r.id}>
                                  <span className="routeIndex" aria-hidden>
                                    {step}
                                  </span>
                                  <div className="routeBox">
                                    <div className="routeBoxHead">
                                      <b>{friendly || r.provider_name}</b>
                                      <span className="routeTags">
                                        <StatusBadge status={r.status} />
                                      </span>
                                    </div>
                                    {friendly && <span className="routeProvider mono">{r.provider_name}</span>}
                                    <code className="routeModel mono">{r.upstream_model}</code>
                                    <div className="routeMeta">
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
                              )
                            })}
                          </div>
                        </div>
                      )
                    })}
                    {!rs.length && (
                      <EmptyState
                        title="该模型尚未配置线路"
                        description={
                          canAddRoute
                            ? '添加至少一条线路后，业务请求才能被路由到这个模型。'
                            : '请先在渠道页添加渠道，再回来配置线路。'
                        }
                      />
                    )}
                  </div>

                  <button
                    className="addRoute"
                    onClick={() => addRoute(m, rs)}
                    disabled={!canAddRoute}
                    title={canAddRoute ? undefined : '请先添加渠道'}
                  >
                    <Plus aria-hidden />
                    添加线路
                  </button>
                </article>
              )
            })}
          </div>
        ) : (
          <EmptyState
            icon={<Route />}
            title="暂无对外模型"
            description="先创建对外模型，再为它添加线路。模型没有线路时不会接收请求。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}>
                新增模型
              </Button>
            }
          />
        )}
      </Panel>

      {drawer?.kind === 'model' && (
        <Drawer
          title={drawer.draft.id ? '编辑对外模型' : '新增对外模型'}
          subtitle="这里只登记对外的模型名；渠道与上游模型在线路中配置。"
          onClose={() => setDrawer(null)}
        >
          <ModelForm value={drawer.draft} onSave={v => void saveModel(v)} onCancel={() => setDrawer(null)} />
        </Drawer>
      )}

      {drawer?.kind === 'route' && (
        <Drawer
          title={drawer.draft.id ? '编辑线路' : '新增线路'}
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

      {drawer?.kind === 'test' && <ModelTestDrawer models={models} initialModel={drawer.model} onClose={() => setDrawer(null)} />}
    </div>
  )
}
