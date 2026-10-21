import {useCallback, useEffect, useState} from 'react'
import {Boxes, Info} from 'lucide-react'
import {api, type Provider, type ProviderModel, type PublishResult} from '../../api'
import {Drawer} from '../ui/Drawer'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {useToast} from '../ui/Toast'

export function PublishModelWizard({onClose, onPublished}: {onClose: () => void; onPublished: () => void}) {
  const toast = useToast()
  const [providers, setProviders] = useState<Provider[]>([])
  const [providerID, setProviderID] = useState('')
  const [candidates, setCandidates] = useState<ProviderModel[]>([])
  const [candidateID, setCandidateID] = useState('')
  const [alias, setAlias] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [description, setDescription] = useState('')
  const [append, setAppend] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingCandidates, setLoadingCandidates] = useState(false)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    setLoading(true)
    api<{data: Provider[]}>('/admin/providers?status=1&limit=200')
      .then(d => {
        if (!live) return
        const list = d.data || []
        setProviders(list)
        setProviderID(list[0]?.id || '')
      })
      .catch(e => {
        if (live) setError(e instanceof Error ? e.message : '无法加载上游渠道')
      })
      .finally(() => {
        if (live) setLoading(false)
      })
    return () => {
      live = false
    }
  }, [])

  const loadCandidates = useCallback(async (id: string) => {
    if (!id) return
    setLoadingCandidates(true)
    setCandidates([])
    setCandidateID('')
    setAlias('')
    try {
      const d = await api<{data: ProviderModel[]}>(`/admin/providers/${id}/models`)
      const usable = (d.data || []).filter(m => m.status === 1)
      setCandidates(usable)
      if (usable[0]) {
        setCandidateID(usable[0].id)
        setAlias(usable[0].upstream_model)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : '无法加载候选模型')
    } finally {
      setLoadingCandidates(false)
    }
  }, [])

  useEffect(() => {
    void loadCandidates(providerID)
  }, [providerID, loadCandidates])

  const selectCandidate = (id: string) => {
    setCandidateID(id)
    const chosen = candidates.find(c => c.id === id)
    if (chosen && !alias.trim()) setAlias(chosen.upstream_model)
  }

  const submit = async () => {
    const candidate = candidates.find(c => c.id === candidateID)
    if (!providerID) {
      setError('请选择上游渠道。')
      return
    }
    if (!candidate) {
      setError('请选择候选模型；如果该渠道没有候选模型，请先在渠道详情中刷新或手动添加。')
      return
    }
    if (!alias.trim()) {
      setError('对外名称（alias）不能为空。')
      return
    }
    setBusy(true)
    setError('')
    setConflict(false)
    try {
      const out = await api<PublishResult>('/admin/models/publish', {
        method: 'POST',
        body: JSON.stringify({
          provider_model_id: candidate.id,
          alias: alias.trim(),
          display_name: displayName.trim(),
          description: description.trim(),
          append_to_existing: append,
        }),
      })
      toast.success(
        out.created
          ? `已发布对外模型 ${out.model.alias}`
          : out.route_created
            ? `已为 ${out.model.alias} 追加备用线路`
            : `${out.model.alias} 已存在相同绑定，无需重复发布`,
      )
      onPublished()
      onClose()
    } catch (e) {
      const apiError = e as {status?: number; message?: string}
      if (apiError.status === 409) {
        setConflict(true)
        setError('该对外名称已存在且绑定了其他渠道/上游模型。确认要追加为备用线路后，勾选下方选项再提交。')
      } else {
        setError(apiError.message || '发布失败')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Drawer
      title="发布模型"
      subtitle="从上游渠道的候选模型创建一个对外模型；发布不会自动授权给任何 API Key。"
      size="md"
      onClose={onClose}
      footer={
        <div className="drawerActionFooter">
          <Button onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={loading || !providerID || !candidateID}
            onClick={() => void submit()}
          >
            {conflict ? '确认发布' : '发布'}
          </Button>
        </div>
      }
    >
      <div className="drawerFormStack">
        <div className="notice info" role="status">
          <Info aria-hidden />
          <span>首先选择上游渠道及其提供的模型，之后可在「路由策略」中追加备用或加权线路。</span>
        </div>

        <Field label="上游渠道" hint="选择提供模型的上游渠道（仅显示已启用渠道）。">
          <select
            value={providerID}
            disabled={loading}
            onChange={e => {
              setProviderID(e.target.value)
              setError('')
            }}
          >
            {!providers.length && <option value="">暂无可用的上游渠道</option>}
            {providers.map(p => (
              <option key={p.id} value={p.id}>
                {p.name}
                {p.provider_type ? `（${p.provider_type}）` : ''}
              </option>
            ))}
          </select>
        </Field>

        <Field
          label="候选模型"
          hint={
            loadingCandidates
              ? '正在加载候选模型…'
              : candidates.length
                ? '该渠道下的可用模型列表，请先到上游渠道页面完成配置。'
                : '该渠道没有可用候选模型；请先到渠道详情 → 模型列表刷新或手动添加。'
          }
        >
          <select
            value={candidateID}
            disabled={loadingCandidates || !candidates.length}
            onChange={e => selectCandidate(e.target.value)}
          >
            {!candidates.length && <option value="">暂无候选模型</option>}
            {candidates.map(c => (
              <option key={c.id} value={c.id}>
                {c.upstream_model}
              </option>
            ))}
          </select>
        </Field>

        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>对外名称（alias）</span>
              <small className="charCountHint">{alias.length}/64</small>
            </div>
          }
          hint="业务系统调用 /v1/* 时使用的 model 值，发布后在服务端不可改名。"
        >
          <input
            className="mono"
            maxLength={64}
            value={alias}
            onChange={e => setAlias(e.target.value)}
            placeholder="例如：campus-gpt"
          />
        </Field>

        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>显示名称（可选）</span>
              <small className="charCountHint">{displayName.length}/50</small>
            </div>
          }
          hint="只用于管理台展示，不改变客户端调用名。"
        >
          <input
            maxLength={50}
            value={displayName}
            onChange={e => setDisplayName(e.target.value)}
            placeholder="例如：校园通用大语言模型"
          />
        </Field>

        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>描述（可选）</span>
              <small className="charCountHint">{description.length}/200</small>
            </div>
          }
          hint="简要说明适用场景，不影响运行时行为。"
        >
          <textarea
            rows={3}
            maxLength={200}
            value={description}
            onChange={e => setDescription(e.target.value)}
            placeholder="面向全校的通用大语言模型，适用于问答、写作、分析等多种场景。"
          />
        </Field>

        {conflict && (
          <div className="checks">
            <label>
              <input type="checkbox" checked={append} onChange={e => setAppend(e.target.checked)} />
              确认把此候选追加为备用线路
            </label>
          </div>
        )}

        {error && <div className="notice warning" role="alert">{error}</div>}
      </div>
    </Drawer>
  )
}
