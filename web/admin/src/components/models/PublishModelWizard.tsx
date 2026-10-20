import {useCallback, useEffect, useState} from 'react'
import {Boxes} from 'lucide-react'
import {api, type Provider, type ProviderModel, type PublishResult} from '../../api'
import {Modal} from '../ui/Modal'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {useToast} from '../ui/Toast'

/**
 * Publish-from-the-model-list wizard.
 *
 * It is the same operation as publishing from a channel's candidate list: choose
 * the channel and candidate, name the external alias, and let the existing
 * POST /admin/models/publish create the model and its first route. The backend
 * keeps its validation, permission check and alias-immutability rule; this
 * component only removes a navigation step.
 */
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
    <Modal
      title="发布模型"
      subtitle="从上游渠道的候选模型创建一个对外模型；发布不会自动授权给任何 API Key。"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" loading={busy} disabled={loading || !providerID || !candidateID} onClick={() => void submit()}>
            {conflict ? '确认发布' : '发布'}
          </Button>
        </>
      }
    >
      <div className="form">
        <Notice>
          <Boxes aria-hidden />
          首条线路使用所选候选的上游模型；之后可在「路由策略」中追加备用或加权线路。
        </Notice>
        <Field label="上游渠道" hint="只列出已启用的渠道。">
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
                ? '来自该渠道的模型列表。'
                : '该渠道没有可用候选模型；请先到渠道详情 → 模型列表刷新或手动添加。'
          }
        >
          <select value={candidateID} disabled={loadingCandidates || !candidates.length} onChange={e => selectCandidate(e.target.value)}>
            {!candidates.length && <option value="">暂无候选模型</option>}
            {candidates.map(c => (
              <option key={c.id} value={c.id}>
                {c.upstream_model}
              </option>
            ))}
          </select>
        </Field>
        <Field label="对外名称（alias）" hint="业务系统调用 /v1/* 时使用的 model 值。发布后在服务端不可改名。">
          <input className="mono" value={alias} onChange={e => setAlias(e.target.value)} />
        </Field>
        <Field label="显示名称（可选）" hint="只用于管理台展示，不改变客户端调用名。">
          <input value={displayName} onChange={e => setDisplayName(e.target.value)} />
        </Field>
        <Field label="描述（可选）">
          <input value={description} onChange={e => setDescription(e.target.value)} />
        </Field>
        {conflict && (
          <div className="checks">
            <label>
              <input type="checkbox" checked={append} onChange={e => setAppend(e.target.checked)} />
              该名称已存在，确认把此候选追加为备用线路
            </label>
          </div>
        )}
        {error && <div className="notice warning" role="alert">{error}</div>}
      </div>
    </Modal>
  )
}
