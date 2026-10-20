import {useCallback, useState} from 'react'
import {KeyRound} from 'lucide-react'
import {api, type AuthorizationResult, type ModelAuthorization} from '../../api'
import {Button} from '../ui/Button'
import {Badge} from '../ui/Badge'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {SkeletonRows} from '../ui/Skeleton'
import {Table} from '../ui/Table'
import {Notice} from '../ui/Field'
import {useToast} from '../ui/Toast'
import {useResource} from '../../lib/useResource'

const outcomeLabel: Record<string, string> = {
  added: '已添加',
  already_allowed: '已允许（无需修改）',
  removed: '已撤销',
  not_present: '原本未允许',
  wildcard_refused: '通配符 Key，未修改',
  not_found: 'Key 不存在',
}

/**
 * Model-scoped authorization. The server merges one alias into the latest
 * allowed_models; it never replaces the whole list from the browser.
 */
export function ModelAuthorizationPanel({modelID, canAuthorize}: {modelID: string; canAuthorize: boolean}) {
  const toast = useToast()
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)
  const [results, setResults] = useState<AuthorizationResult[]>([])
  const loader = useCallback(async () => {
    const d = await api<{alias: string; data: ModelAuthorization[]}>(`/admin/models/${modelID}/authorizations`)
    return d.data || []
  }, [modelID])
  const {data, loading, error, errorStatus, reload} = useResource(loader, [modelID])

  const toggle = (id: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const apply = async (allow: boolean) => {
    if (!selected.size) return
    setBusy(true)
    setResults([])
    try {
      const out = await api<{results: AuthorizationResult[]}>(`/admin/models/${modelID}/authorizations`, {
        method: 'PUT',
        body: JSON.stringify({api_key_ids: [...selected], allow}),
      })
      setResults(out.results || [])
      const refused = (out.results || []).filter(r => r.outcome === 'wildcard_refused').length
      if (refused) {
        toast.info(`${refused} 个通配符 Key 未被修改；如需限制单个模型，请到 Key 页面显式转为白名单。`)
      } else {
        toast.success(allow ? '授权已更新' : '撤销已更新')
      }
      setSelected(new Set())
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '更新模型授权失败')
    } finally {
      setBusy(false)
    }
  }

  const items = data || []

  return (
    <div className="drawerStack">
      <Notice>
        <span>
          授权针对具体 API Key，不是整个租户。服务端只在最新白名单中增删此模型，不会覆盖其他模型的授权。
          <code>[]</code> 表示不允许任何模型，<code>[*]</code> 表示允许全部；通配符不会被静默改写。
        </span>
      </Notice>
      {!canAuthorize && <p className="hint">只有超级管理员可以修改模型授权。</p>}

      {loading && !data ? (
        <SkeletonRows rows={4} columns={5} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : items.length ? (
        <>
          <Table heads={['选择', 'Key', '租户', '状态', '当前授权', '最后使用']} minWidth={680}>
            {items.map(k => (
              <tr key={k.api_key_id}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`选择 Key ${k.name}`}
                    checked={selected.has(k.api_key_id)}
                    disabled={!canAuthorize}
                    onChange={() => toggle(k.api_key_id)}
                  />
                </td>
                <td>
                  <b>{k.name}</b>
                  {k.wildcard ? <Badge tone="warning">通配符</Badge> : null}
                </td>
                <td>{k.tenant_name}</td>
                <td>{k.status === 1 ? '启用' : '停用'}</td>
                <td>{k.wildcard ? '全部模型' : k.allowed ? '已允许此模型' : '未允许'}</td>
                <td>{k.last_used_at || '—'}</td>
              </tr>
            ))}
          </Table>
          <div className="formActions">
            <Button disabled={!canAuthorize || busy || !selected.size} loading={busy} onClick={() => void apply(true)}>
              授权所选 Key
            </Button>
            <Button variant="danger" disabled={!canAuthorize || busy || !selected.size} onClick={() => void apply(false)}>
              撤销所选 Key
            </Button>
          </div>
        </>
      ) : (
        <EmptyState icon={<KeyRound />} title="暂无 API Key" description="先在调用管理中创建 Key，再授权模型。" />
      )}

      {results.length > 0 && (
        <div className="notice info">
          <b>逐条结果</b>
          <ul className="drawerSectionList">
            {results.map(r => (
              <li key={r.api_key_id}>
                <span className="mono">{r.api_key_id.slice(0, 8)}…</span>：{outcomeLabel[r.outcome] || r.outcome}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
