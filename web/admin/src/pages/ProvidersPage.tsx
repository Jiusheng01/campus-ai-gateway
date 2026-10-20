import {useState} from 'react'
import {Network, Pencil, Plus, Trash2} from 'lucide-react'
import {api, type CatalogModel, type ModelConfig, type ModelRoute, type Provider, type ProviderDeleteResult} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ProviderForm, type ProviderDraft} from '../components/providers/ProviderForm'
import {friendlyProviderName, protocolLabel} from '../components/providers/templates'
import {useResource} from '../lib/useResource'

/**
 * deleteCost is the price the confirmation quotes: how many Routes the channel
 * serves and how many Virtual Models lose their last Route with it.
 *
 * It is read from the same Route endpoints the models page uses rather than
 * from a dedicated preview endpoint, so the number shown is the configuration
 * the operator is looking at. The delete response reports what actually
 * happened; the preview is only the warning.
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

/**
 * ServedLines lists, read-only, the lines this channel has opened through the
 * Model Catalog.
 *
 * A Route owns the upstream model, never a Provider (ADR-0018), so the channel
 * form has no field for it. The catalog is built from gateway_provider_model,
 * and `opened` on a catalog provider means the (model, channel, upstream)
 * binding has a Route. That definition has two blind spots this section must not
 * hide: a Route whose upstream was entered free-text on the 模型 page has no
 * gateway_provider_model row, and the catalog excludes disabled channels (and
 * disabled provider models). The catalog also carries no route priority, so the
 * list makes no claim about which line is primary — that ordering and the
 * complete set live in the 模型 page's advanced view.
 */
function ServedLines({
  provider,
  catalog,
  catalogError,
}: {
  provider: Partial<Provider>
  catalog: CatalogModel[]
  catalogError: string
}) {
  const lines = catalog
    .filter(m => m.providers.some(p => p.id === provider.id && p.opened))
    .map(m => m.upstream_model)

  return (
    <section className="drawerSection" aria-label="通过模型目录开放的线路">
      <h3 className="drawerSectionTitle">通过模型目录开放的线路（只读）</h3>
      {catalogError ? (
        <p className="hint">线路信息暂时无法加载：{catalogError}。渠道本身仍可正常编辑。</p>
      ) : lines.length ? (
        <ul className="drawerSectionList">
          {lines.map(upstream => (
            <li key={upstream}>
              <span className="muted">本渠道 →</span>
              <code className="mono">{upstream}</code>
            </li>
          ))}
        </ul>
      ) : (
        <p className="hint">
          {provider.status === 1
            ? '该渠道尚未通过模型目录开放任何线路。'
            : '渠道已停用，停用的渠道不会出现在模型目录中。'}
          在「模型」页手工填写的线路不会显示在这里；请到「模型」页查看完整线路。
        </p>
      )}
      <p className="hint">
        这里只列出通过模型目录开放的线路。上游模型属于线路，不在本表单中编辑；手工填写的线路与停用渠道的线路请到「模型」页的「对外模型与线路（高级）」中查看。
      </p>
    </section>
  )
}

export function ProvidersPage() {
  const toast = useToast()
  const confirm = useConfirm()
  // The channel list drives the page; the catalog only enriches the drawer.
  // Loading them together would let a catalog failure blank a page whose own
  // data arrived, which is the opposite of the degradation being shown.
  const {
    data: providers,
    loading,
    error,
    errorStatus,
    reload: reloadProviders,
  } = useResource(async () => (await api<{data: Provider[]}>('/admin/providers')).data || [])
  const {data: catalogData, error: catalogError, reload: reloadCatalog} = useResource(
    async () => (await api<{data: CatalogModel[]}>('/admin/model-catalog')).data || [],
  )
  const reload = () => {
    reloadProviders()
    reloadCatalog()
  }
  const [draft, setDraft] = useState<ProviderDraft | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)

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

  const items = providers || []
  const catalog = catalogData || []

  return (
    <>
      <Panel
        title="渠道管理"
        sub="配置模型渠道的接入地址与凭据来源：可以直接粘贴厂商 API Key（加密保存、只写不读），也可以改为引用环境变量。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1, protocol: 'openai-chat'})}>
            新增渠道
          </Button>
        }
      >
        {loading && !providers ? (
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
                      {friendly && <span className="providerFriendly">{friendly}</span>}
                      <h3 className="providerName">{p.name}</h3>
                    </div>
                    <StatusBadge status={p.status} />
                  </header>

                  <dl className="providerRows">
                    <div className="providerRow">
                      <dt>接口协议</dt>
                      <dd>{protocolLabel(p.protocol)}</dd>
                    </div>
                    <div className="providerRow">
                      <dt>Base URL</dt>
                      <dd className="mono cut" title={p.base_url}>
                        {p.base_url || '—'}
                      </dd>
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
                  </dl>

                  <footer className="providerFoot">
                    <Button size="sm" icon={<Pencil />} onClick={() => setDraft(p)}>
                      编辑
                    </Button>
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
            description="添加渠道后即可配置模型线路。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1, protocol: 'openai-chat'})}>
                新增渠道
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? '编辑渠道' : '新增渠道'}
          subtitle="密钥加密保存、只写不读；也可以改为引用环境变量。"
          onClose={() => setDraft(null)}
        >
          {draft.id && <ServedLines provider={draft} catalog={catalog} catalogError={catalogError} />}
          <ProviderForm value={draft} onSave={v => void save(v)} onCancel={() => setDraft(null)} />
        </Drawer>
      )}
    </>
  )
}
