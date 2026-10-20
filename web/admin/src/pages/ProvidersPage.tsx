import {useState} from 'react'
import {Network, Pencil, Plus} from 'lucide-react'
import {api, type Provider} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {useToast} from '../components/ui/Toast'
import {ProviderForm, type ProviderDraft} from '../components/providers/ProviderForm'
import {friendlyProviderName, protocolLabel} from '../components/providers/templates'
import {useResource} from '../lib/useResource'

export function ProvidersPage() {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: Provider[]}>('/admin/providers')
    return d.data || []
  })
  const [draft, setDraft] = useState<ProviderDraft | null>(null)

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

  const items = data || []

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
          <ProviderForm value={draft} onSave={v => void save(v)} onCancel={() => setDraft(null)} />
        </Drawer>
      )}
    </>
  )
}
