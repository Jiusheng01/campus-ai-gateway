import {useState} from 'react'
import {Ban, KeyRound, Pencil, Plus} from 'lucide-react'
import {api, revokeApiKey, type ApiKey, type ModelConfig, type Tenant} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {KeyForm, type KeyDraft} from '../components/keys/KeyForm'
import {SecretModal} from '../components/keys/SecretModal'
import {useResource} from '../lib/useResource'
import {ago, fmt, num} from '../lib/format'

type Bundle = {keys: ApiKey[]; tenants: Tenant[]; models: ModelConfig[]}

export function ApiKeysPage() {
  const toast = useToast()
  const confirm = useConfirm()
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
  const [revoking, setRevoking] = useState<string | null>(null)

  const save = async (v: KeyDraft) => {
    try {
      const out = await api<{api_key?: string}>(v.id ? `/admin/api-keys/${v.id}` : '/admin/api-keys', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success(v.id ? 'API 密钥已更新' : 'API 密钥已创建')
      setDraft(null)
      if (out.api_key) setSecret(out.api_key)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存 API 密钥失败')
    }
  }

  /**
   * Revoking is not the same as the 停用 option in the edit drawer, and the
   * confirmation says so: 停用 can be undone, revocation cannot. The gateway
   * replaces the stored digest, so the key that was handed out will never
   * authenticate again even if the row is re-enabled later.
   */
  const revoke = async (k: ApiKey) => {
    const ok = await confirm({
      title: `吊销「${k.name}」？`,
      description: `吊销后 ${k.key_prefix}… 会立即失效，且无法恢复——重新启用也不会让旧 Key 再次可用。历史使用日志会保留，如需继续使用请创建新的 Key。`,
      confirmText: '确认吊销',
      danger: true,
    })
    if (!ok) return
    setRevoking(k.id)
    try {
      await revokeApiKey(k.id)
      toast.success(`已吊销「${k.name}」`)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '吊销失败')
    } finally {
      setRevoking(null)
    }
  }

  const keys = data?.keys || []
  const canCreate = !!data?.tenants.length

  return (
    <>
      <Panel
        title="API 密钥"
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
          <SkeletonRows rows={5} columns={9} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : keys.length ? (
          <Table
            heads={['名称', '租户', 'Key 前缀', '状态', '允许模型', 'RPM', 'TPM', '已用请求', '已用 Token', '最后使用', '操作']}
            minWidth={1380}
          >
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
                {/* Cumulative, aggregated from the request log rather than from
                    a counter on the key, so it can never disagree with the log
                    it summarises. */}
                <td className="numeric">{num(k.usage_requests)}</td>
                <td className="numeric">{fmt(k.usage_tokens)}</td>
                <td>{ago(k.last_used_at)}</td>
                <td className="rowActions">
                  <button className="link" onClick={() => setDraft(k)}>
                    <Pencil aria-hidden />
                    编辑
                  </button>
                  <button
                    className="link dangerText"
                    disabled={revoking === k.id}
                    onClick={() => void revoke(k)}
                  >
                    <Ban aria-hidden />
                    {revoking === k.id ? '吊销中…' : '吊销'}
                  </button>
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState
            icon={<KeyRound />}
            title="暂无 API 密钥"
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
          title={draft.id ? '编辑 API 密钥' : '创建 API 密钥'}
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
