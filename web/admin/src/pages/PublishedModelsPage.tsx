import {useCallback, useState} from 'react'
import {Boxes, ChevronRight, FlaskConical, Route} from 'lucide-react'
import {api, type ModelConfig, type ModelDetail, type ModelImpact, type ModelRoute} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Detail} from '../components/ui/Detail'
import {Modal} from '../components/ui/Modal'
import {RouteEditor} from '../components/models/RouteEditor'
import {ModelAuthorizationPanel} from '../components/models/ModelAuthorizationPanel'
import {RuntimeSummaryPanel, type LogSeed} from '../components/models/RuntimeSummaryPanel'
import {useToast} from '../components/ui/Toast'
import {useResource} from '../lib/useResource'

/**
 * The external-model page is the client-facing view: an alias is what a business
 * system calls, while the channel and upstream model belong to its lines.
 *
 * MS1 has no model-kind contract. The page therefore says "未知" instead of
 * claiming every legacy alias is a chat model.
 */
export function PublishedModelsPage({
  canAuthorize = false,
  canOpenRawLogs = false,
  onOpenLogs,
}: {
  canAuthorize?: boolean
  canOpenRawLogs?: boolean
  onOpenLogs?: (filters: LogSeed) => void
}) {
  const loader = useCallback(async () => {
    const d = await api<{data: ModelConfig[]}>('/admin/models')
    return d.data || []
  }, [])
  const {data, loading, error, errorStatus, reload} = useResource(loader)
  const [detail, setDetail] = useState<ModelDetail | null>(null)
  const toast = useToast()
  const [detailBusy, setDetailBusy] = useState('')
  const [detailError, setDetailError] = useState('')
  const [editingRoutes, setEditingRoutes] = useState(false)
  const [impact, setImpact] = useState<ModelImpact | null>(null)
  const [impactError, setImpactError] = useState('')
  const [disableBusy, setDisableBusy] = useState(false)

  const loadImpact = async (m: ModelConfig) => {
    setImpactError('')
    try {
      setImpact(await api<ModelImpact>(`/admin/models/${m.id}/impact`))
    } catch (e) {
      setImpactError(e instanceof Error ? e.message : '无法读取停用影响')
    }
  }

  const disableModel = async () => {
    if (!detail) return
    setDisableBusy(true)
    try {
      await api(`/admin/models/${detail.model.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          alias: detail.model.alias,
          display_name: detail.model.display_name,
          description: detail.model.description,
          status: 2,
          config_version: detail.version,
        }),
      })
      toast.success('模型已停用；已发布 alias 不会自动从 Key 白名单移除')
      setImpact(null)
      await open(detail.model)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '停用模型失败')
    } finally {
      setDisableBusy(false)
    }
  }

  const open = async (m: ModelConfig) => {
    setDetailBusy(m.id)
    setDetailError('')
    try {
      const d = await api<ModelDetail>(`/admin/models/${m.id}`)
      setDetail(d)
    } catch (e) {
      setDetailError(e instanceof Error ? e.message : '无法加载模型详情')
    } finally {
      setDetailBusy('')
    }
  }

  const models = data || []

  return (
    <>
      <Panel
        title="对外模型"
        sub="业务系统只使用这里的外部名称。渠道、上游模型和故障切换都由每个模型的线路决定。"
      >
        {loading && !data ? (
          <SkeletonCards count={3} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : models.length ? (
          <div className="modelCards">
            {models.map(m => (
              <article className={`modelCard ${m.status === 1 ? '' : 'disabled'}`} key={m.id}>
                <header className="modelHead">
                  <div className="modelIdentity">
                    <div className="modelAliasRow">
                      <h3 className="modelAlias mono">{m.alias}</h3>
                      <StatusBadge status={m.status} />
                    </div>
                    <span className="modelKind">{m.display_name || '未设置显示名称'}</span>
                    <p className="modelHint">
                      业务系统使用 <code className="mono">model={m.alias}</code>
                    </p>
                  </div>
                  <div className="modelActions">
                    <Button size="sm" icon={<ChevronRight />} loading={detailBusy === m.id} onClick={() => void open(m)}>
                      查看详情
                    </Button>
                  </div>
                </header>

                <dl className="providerRows">
                  <div className="providerRow">
                    <dt>配置完整性</dt>
                    <dd>
                      {m.statically_routable ? <Badge tone="success">至少一条静态可路由线路</Badge> : <Badge tone="warning">暂无静态可路由线路</Badge>}
                    </dd>
                  </div>
                  <div className="providerRow">
                    <dt>线路</dt>
                    <dd>
                      {m.enabled_route_count ?? 0} / {m.route_count ?? 0} 条启用
                      {m.routing_mode_summary ? <span className="muted"> · {m.routing_mode_summary}</span> : null}
                    </dd>
                  </div>
                  <div className="providerRow">
                    <dt>凭据</dt>
                    <dd>{m.credential_configured ? '来源已配置' : '未知或未配置'}</dd>
                  </div>
                  <div className="providerRow">
                    <dt>服务类型</dt>
                    <dd>未知（MS1 未建立类型契约）</dd>
                  </div>
                  <div className="providerRow">
                    <dt>配置版本</dt>
                    <dd className="mono">{m.config_version}</dd>
                  </div>
                </dl>
              </article>
            ))}
          </div>
        ) : (
          <EmptyState
            icon={<Boxes />}
            title="暂无对外模型"
            description="先在上游渠道中维护候选模型，再从候选模型发布对外名称。"
          />
        )}
      </Panel>

      {detail && (
        <Drawer title={detail.model.alias} subtitle="对外模型详情" onClose={() => setDetail(null)}>
          <div className="drawerStack">
            <Detail
              title="基本信息"
              rows={[
                ['对外名称（alias）', <span key="alias" className="mono">{detail.model.alias}</span>],
                ['显示名称', detail.model.display_name || '—'],
                ['描述', detail.model.description || '—'],
                ['状态', detail.model.status === 1 ? '启用' : '停用'],
                ['配置版本', <span key="version" className="mono">{detail.version}</span>],
                ['服务类型', '未知（MS1 未建立类型契约）'],
              ]}
            />
            {detail.model.status === 1 && (
              <Panel title="下线模型" sub="停用后该 alias 不再静态可路由；已授权的 Key 不会被自动改写。">
                <Button variant="danger" onClick={() => void loadImpact(detail.model)}>查看停用影响</Button>
              </Panel>
            )}
            <Panel
              title="线路"
              sub="线路是唯一的运行时路由权威。"
              actions={<Button size="sm" onClick={() => setEditingRoutes(true)}>编辑线路</Button>}
            >
              {detail.routes.length ? (
                <div className="routeChain">
                  {detail.routes.map((r: ModelRoute) => (
                    <div className="routeBox" key={r.id}>
                      <div className="routeBoxHead">
                        <b>{r.provider_name}</b>
                        <span className="routeTags">
                          <Badge tone="info">优先级 {r.priority}</Badge>
                          <StatusBadge status={r.status} />
                        </span>
                      </div>
                      <code className="routeModel mono">{r.upstream_model}</code>
                      <div className="routeMeta">
                        <span>权重 <b>{r.weight}</b></span>
                        <span>超时 <b>{r.timeout_ms} ms</b></span>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState icon={<Route />} title="暂无线路" description="该模型尚不可路由。" />
              )}
            </Panel>
            <Panel title="授权" sub="授权针对具体 API Key；发布模型不会自动授权。">
              <ModelAuthorizationPanel modelID={detail.model.id} canAuthorize={canAuthorize} />
            </Panel>
            <Panel title="使用记录" sub="脱敏运行摘要；原始日志仍由超级管理员/审计员读取。">
              <RuntimeSummaryPanel
                kind="model"
                id={detail.model.id}
                canOpenRawLogs={canOpenRawLogs}
                onOpenLogs={onOpenLogs}
              />
            </Panel>
            <p className="responseText muted">
              <FlaskConical aria-hidden /> 模型诊断测试保留在独立的管理测试入口。
            </p>
          </div>
        </Drawer>
      )}

      {impact && (
        <Modal
          title={`停用模型：${impact.alias}`}
          subtitle="这是影响预览；提交后服务端会重新校验并立即停用。"
          onClose={() => setImpact(null)}
          footer={
            <>
              <Button onClick={() => setImpact(null)}>取消</Button>
              <Button variant="danger" loading={disableBusy} onClick={() => void disableModel()}>确认停用</Button>
            </>
          }
        >
          <div className="drawerStack">
            <p>启用线路：{impact.enabled_routes} 条；启用渠道：{impact.enabled_providers} 个。</p>
            <p>显式允许该 alias 的 Key：{impact.explicit_keys} 个；通配符 Key：{impact.wildcard_keys} 个。</p>
            <p className="hint">停用不会修改任何 Key 的白名单。通配符 Key 仍允许全部模型，但该 alias 不再出现在静态可路由集合中。</p>
            <p className="hint">本实例保存后立即生效；其他实例在 5 秒 TTL 内收敛；数据库故障时可能继续使用旧快照。</p>
          </div>
        </Modal>
      )}

      {impactError && (
        <div className="notice warning" role="alert">{impactError}</div>
      )}

      {editingRoutes && detail && (
        <Modal
          title={`编辑线路：${detail.model.alias}`}
          subtitle="整组保存，带配置版本校验。"
          onClose={() => setEditingRoutes(false)}
        >
          <RouteEditor
            modelID={detail.model.id}
            onSaved={() => {
              void open(detail.model)
              reload()
            }}
          />
        </Modal>
      )}

      {detailError && !detail && (
        <div className="notice warning" role="alert">
          {detailError}
        </div>
      )}
    </>
  )
}
