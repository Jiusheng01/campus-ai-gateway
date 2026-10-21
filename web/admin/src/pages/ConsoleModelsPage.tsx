import {useState} from 'react'
import {
  Building2,
  Check,
  Copy,
  KeyRound,
  ServerOff,
  ShieldOff,
} from 'lucide-react'
import {getConsoleModels, type ConsoleModels} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {Panel} from '../components/ui/Panel'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Button} from '../components/ui/Button'
import {copyText} from '../lib/format'
import {useResource} from '../lib/useResource'
import {useToast} from '../components/ui/Toast'

/**
 * The personal model catalog.
 *
 * Every row comes from /api/console/models, which already intersected the
 * Tenant's allowlist with the statically routable set. The page therefore never
 * decides whether a model is callable: it renders what the gateway says is
 * callable, and names the reason when there is nothing to show.
 *
 * The access details are derived from the page's own origin rather than a
 * configured host, because the browser reached the gateway by that origin and
 * /v1 is the data plane's fixed prefix. No key material is stored or fetched
 * here; the example uses the variable a member sets from their own personal key.
 */
export function ConsoleModelsPage() {
  const {data, loading, error, errorStatus, reload} = useResource(getConsoleModels)
  const toast = useToast()
  const [selectedAlias, setSelectedAlias] = useState('')
  const [copied, setCopied] = useState('')

  const models = data?.models || []
  // Keep the selection honest across a reload: if the chosen alias disappeared,
  // fall back to the first available one rather than rendering a dead example.
  const alias = models.some(m => m.alias === selectedAlias)
    ? selectedAlias
    : models[0]?.alias || ''
  const baseUrl = `${window.location.origin}/v1`

  const curlExample = `curl ${baseUrl}/chat/completions \\
  -H "Authorization: Bearer $CAMPUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${alias}",
    "messages": [{"role": "user", "content": "你好"}]
  }'`

  const sdkExample = `import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["CAMPUS_API_KEY"],
    base_url="${baseUrl}",
)

resp = client.chat.completions.create(
    model="${alias}",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)`

  async function copy(kind: string, value: string, label: string) {
    if (await copyText(value)) {
      setCopied(kind)
      toast.success(`${label}已复制到剪贴板`)
      return
    }
    toast.error('复制失败，请手动选择并复制')
  }

  return (
    <div className="stack">
      <PageBanner
        title="模型"
        subtitle="你的租户允许调用的模型，以及使用个人 API Key 接入的方式。"
        illustration={false}
      />

      {error ? (
        <Panel title="模型">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : loading && !data ? (
        <Panel title="模型">
          <SkeletonRows rows={4} columns={3} />
        </Panel>
      ) : (
        <>
          <Panel title="可用模型" sub="仅列出已启用且当前有可用线路的模型。">
            {models.length ? (
              <Table heads={['模型别名', '显示名称', '说明']} minWidth={620}>
                {models.map(m => (
                  <tr key={m.alias}>
                    <td>
                      <code className="mono">{m.alias}</code>
                    </td>
                    <td>{m.display_name || '—'}</td>
                    <td>{m.description || '—'}</td>
                  </tr>
                ))}
              </Table>
            ) : (
              <CatalogEmptyState data={data} />
            )}
          </Panel>

          {models.length > 0 && (
            <Panel title="接入信息" sub="OpenAI 兼容接口；请使用你自己的个人 API Key。">
              <div className="panelPad accessStack">
                <div className="accessBlock">
                  <div className="accessHeading">
                    <b>OpenAI 兼容 Base URL</b>
                    <Button
                      size="sm"
                      icon={copied === 'base' ? <Check /> : <Copy />}
                      onClick={() => void copy('base', baseUrl, 'Base URL ')}
                    >
                      {copied === 'base' ? '已复制' : '复制'}
                    </Button>
                  </div>
                  <code className="accessCode mono">{baseUrl}</code>
                  <p className="hint">
                    在业务系统的 OpenAI 客户端中把 base_url 指向该地址，api_key 使用你在「API Key」
                    页面创建的个人凭据。
                  </p>
                </div>

                <div className="accessBlock">
                  <div className="accessHeading">
                    <b>请求示例</b>
                    <div className="accessHeadingActions">
                      <label className="accessModelPick">
                        <span>示例模型</span>
                        <select
                          aria-label="请求示例使用的模型"
                          value={alias}
                          onChange={e => setSelectedAlias(e.target.value)}
                        >
                          {models.map(m => (
                            <option key={m.alias} value={m.alias}>
                              {m.alias}
                            </option>
                          ))}
                        </select>
                      </label>
                      <Button
                        size="sm"
                        icon={copied === 'curl' ? <Check /> : <Copy />}
                        onClick={() => void copy('curl', curlExample, '请求示例')}
                      >
                        {copied === 'curl' ? '已复制' : '复制示例'}
                      </Button>
                    </div>
                  </div>
                  <pre className="helpCodeBlock">
                    <code>{curlExample}</code>
                  </pre>
                </div>

                <div className="accessBlock">
                  <div className="accessHeading">
                    <b>SDK 配置（Python）</b>
                    <Button
                      size="sm"
                      icon={copied === 'sdk' ? <Check /> : <Copy />}
                      onClick={() => void copy('sdk', sdkExample, 'SDK 配置')}
                    >
                      {copied === 'sdk' ? '已复制' : '复制配置'}
                    </Button>
                  </div>
                  <pre className="helpCodeBlock">
                    <code>{sdkExample}</code>
                  </pre>
                  <p className="hint">
                    <KeyRound size={13} aria-hidden /> 示例从环境变量 CAMPUS_API_KEY
                    读取个人 Key；完整 Key 只在创建时显示一次。
                  </p>
                </div>
              </div>
            </Panel>
          )}
        </>
      )}
    </div>
  )
}

/**
 * The three empty states are different facts and must read differently: an
 * unassigned account cannot act, a Tenant at `[]` needs an administrator to
 * widen its allowlist, and an empty deployment needs an administrator to
 * publish a model. Showing one generic "no models" would hide which action is
 * actually needed.
 */
function CatalogEmptyState({data}: {data: ConsoleModels | null}) {
  switch (data?.empty_reason) {
    case 'no_tenant':
      return (
        <EmptyState
          icon={<Building2 />}
          title="尚未分配租户"
          description="你的账号还没有加入任何租户，因此没有可调用的模型。请联系管理员在「用户」中为你的账号分配租户；分配后刷新本页即可看到可用模型。"
        />
      )
    case 'tenant_allows_none':
      return (
        <EmptyState
          icon={<ShieldOff />}
          title="当前租户未开放任何模型"
          description="你的租户把可用模型设置为空，因此暂时不能调用任何模型。请联系管理员在租户设置中开放所需模型。"
        />
      )
    case 'no_eligible_models':
      return (
        <EmptyState
          icon={<ServerOff />}
          title="暂无可调用的模型"
          description="你的租户没有限制模型，但网关当前尚未配置可用的模型线路。请联系管理员启用模型或补充线路后刷新本页。"
        />
      )
    default:
      return (
        <EmptyState
          icon={<ServerOff />}
          title="暂无可调用的模型"
          description="当前没有可用的模型。请稍后刷新，或联系管理员检查模型配置。"
        />
      )
  }
}
