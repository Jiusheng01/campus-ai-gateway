import {useState} from 'react'
import {KeyRound, Pencil, Plus} from 'lucide-react'
import {api, type ApiKey, type ModelConfig, type Tenant} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {KeyForm, type KeyDraft} from '../components/keys/KeyForm'
import {SecretModal} from '../components/keys/SecretModal'
import {useResource} from '../lib/useResource'
import {ago, num} from '../lib/format'

type Bundle = {keys: ApiKey[]; tenants: Tenant[]; models: ModelConfig[]}

export function ApiKeysPage() {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(async (): Promise<Bundle> => {
    const [k, t, m] = await Promise.all([
      api<{data: ApiKey[]}>('/admin/api-keys'),
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
    ])
    return {keys: k.data || [], tenants: t.data || [], models: m.data || []}
  })
  const [draft, setDraft] = useState<KeyDraft | null>(null)
  const [secret, setSecret] = useState<string | null>(null)

  const save = async (v: KeyDraft) => {
    try {
      const out = await api<{api_key?: string}>(v.id ? `/admin/api-keys/${v.id}` : '/admin/api-keys', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success(v.id ? 'API Key 已更新' : 'API Key 已创建')
      setDraft(null)
      if (out.api_key) setSecret(out.api_key)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存 API Key 失败')
    }
  }

  const keys = data?.keys || []
  const canCreate = !!data?.tenants.length

  return (
    <>
      <Panel
        title="API Key"
        sub="业务系统调用 /v1/* 使用的虚拟凭证。完整 Key 只在创建时显示一次，数据库仅保存前缀与 HMAC 摘要。"
        actions={
          <Button
            variant="primary"
            icon={<Plus />}
            disabled={!canCreate}
            title={canCreate ? undefined : '请先创建租户'}
            onClick={() =>
              setDraft({status: 1, rpm_limit: 60, tpm_limit: 100000, max_concurrency: 10, allowed_models: [], allowed_ips: []})
            }
          >
            创建 Key
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonRows rows={5} columns={7} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : keys.length ? (
          <Table heads={['名称', '租户', 'Key 前缀', '状态', '允许模型', 'RPM', 'TPM', '并发', '最后使用', '操作']} minWidth={1180}>
            {keys.map(k => (
              <tr key={k.id}>
                <td>
                  <b>{k.name}</b>
                </td>
                <td>{k.tenant_name || '—'}</td>
                <td className="mono">{k.key_prefix}…</td>
                <td>
                  <StatusBadge status={k.status} />
                </td>
                <td>
                  {(k.allowed_models || []).length ? (
                    (k.allowed_models || []).map(a => <Badge key={a}>{a}</Badge>)
                  ) : (
                    <span className="muted">未授权任何模型</span>
                  )}
                </td>
                <td className="numeric">{num(k.rpm_limit)}</td>
                <td className="numeric">{num(k.tpm_limit)}</td>
                <td className="numeric">{num(k.max_concurrency)}</td>
                <td>{ago(k.last_used_at)}</td>
                <td className="rowActions">
                  <button className="link" onClick={() => setDraft(k)}>
                    <Pencil aria-hidden />
                    编辑
                  </button>
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState
            icon={<KeyRound />}
            title="暂无 API Key"
            description={canCreate ? '创建 Key 后，业务系统即可调用对外模型。' : '请先在「租户」页面创建租户。'}
            action={
              canCreate ? (
                <Button
                  variant="primary"
                  icon={<Plus />}
                  onClick={() =>
                    setDraft({status: 1, rpm_limit: 60, tpm_limit: 100000, max_concurrency: 10, allowed_models: [], allowed_ips: []})
                  }
                >
                  创建 Key
                </Button>
              ) : undefined
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? '编辑 API Key' : '创建 API Key'}
          subtitle="限流与配额在 Key 与租户两级同时生效，取更严格的一方。"
          onClose={() => setDraft(null)}
        >
          <KeyForm
            value={draft}
            tenants={data?.tenants || []}
            models={data?.models || []}
            onSave={v => void save(v)}
            onCancel={() => setDraft(null)}
          />
        </Drawer>
      )}

      {secret && <SecretModal value={secret} onClose={() => setSecret(null)} />}
    </>
  )
}
