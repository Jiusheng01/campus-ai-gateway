import {useEffect, useRef, useState} from 'react'
import {Info} from 'lucide-react'
import {api, type Provider, type ProviderModel, type PublishResult} from '../../api'
import {Drawer} from '../ui/Drawer'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {useToast} from '../ui/Toast'
import {pathFor} from '../../app/routing'

export function PublishModelWizard({onClose, onPublished}: {onClose: () => void; onPublished: (result: PublishResult) => void}) {
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
  const [providerError, setProviderError] = useState('')
  const [candidateError, setCandidateError] = useState('')
  const [providerRetry, setProviderRetry] = useState(0)
  const [candidateRetry, setCandidateRetry] = useState(0)
  const [aliasError, setAliasError] = useState('')
  const submitting = useRef(false)

  useEffect(() => {
    let live = true
    setLoading(true)
    setProviderError('')
    api<{data: Provider[]}>('/admin/providers')
      .then(d => {
        if (!live) return
        const list = (d.data || []).filter(p => p.status === 1)
        setProviders(list)
        setProviderID(list[0]?.id || '')
      })
      .catch(e => {
        if (live) setProviderError(e instanceof Error ? e.message : '无法加载上游渠道')
      })
      .finally(() => {
        if (live) setLoading(false)
      })
    return () => {
      live = false
    }
  }, [providerRetry])

  useEffect(() => {
    let live = true
    if (!providerID) return
    setLoadingCandidates(true)
    setCandidateError('')
    setCandidates([])
    setCandidateID('')
    setAlias('')
    setConflict(false)
    setAppend(false)
    void api<{data: ProviderModel[]}>(`/admin/providers/${providerID}/models`).then(d => {
      if (!live) return
      const usable = (d.data || []).filter(m => m.status === 1)
      setCandidates(usable)
      if (usable[0]) {
        setCandidateID(usable[0].id)
        setAlias(usable[0].upstream_model)
      }
    }).catch(e => {
      if (!live) return
      setCandidateError(e instanceof Error ? e.message : '无法加载候选模型')
    }).finally(() => {
      if (live) setLoadingCandidates(false)
    })
    return () => { live = false }
  }, [providerID, candidateRetry])

  const selectCandidate = (id: string) => {
    const previous = candidates.find(c => c.id === candidateID)
    setCandidateID(id)
    const chosen = candidates.find(c => c.id === id)
    if (chosen && (!alias.trim() || alias === previous?.upstream_model)) setAlias(chosen.upstream_model)
    setConflict(false)
    setAppend(false)
    setError('')
  }

  const submit = async () => {
    if (submitting.current || loadingCandidates) return
    setAliasError('')
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
      setAliasError('对外名称（alias）不能为空。')
      return
    }
    if (Array.from(alias.trim()).length > 128) {
      setAliasError('对外名称（alias）最多 128 个字符。')
      return
    }
    if (conflict && !append) {
      setError('请先确认追加备用线路，或修改对外名称后重新发布。')
      return
    }
    submitting.current = true
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
      onPublished(out)
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
      submitting.current = false
      setBusy(false)
    }
  }

  return (
    <Drawer
      title="发布模型"
      subtitle="选择渠道和上游模型，发布业务系统可调用的稳定名称。"
      size="md"
      onClose={() => { if (!submitting.current) onClose() }}
      dismissable={!busy}
      footer={
        <>
          <Button disabled={busy} onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={loading || loadingCandidates || !providerID || !candidateID || (conflict && !append)}
            onClick={() => void submit()}
          >
            {conflict ? '确认发布' : '发布'}
          </Button>
        </>
      }
    >
      <div className="drawerStack">
        <Notice>
          <Info aria-hidden />
          <span>发布不会自动授权给 API Key。发布后可管理授权或配置备用线路。</span>
        </Notice>
        <fieldset className="formSectionFieldset" disabled={busy}>
        <legend className="formSectionLegend">模型来源</legend>
        <Field label="上游渠道" hint="选择提供模型的上游渠道（仅显示已启用渠道）。">
          <select
            value={providerID}
            disabled={loading}
            onChange={e => {
              setProviderID(e.target.value)
              setError('')
            }}
          >
            {!providers.length && <option value="">{loading ? '正在加载渠道…' : providerError ? '渠道读取失败' : '暂无可用的上游渠道'}</option>}
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
              : candidateError ? '候选模型读取失败，请重试。' : candidates.length
                ? 'Provider Model 是该渠道接收请求时使用的 Upstream Model。'
                : '暂无启用的候选模型，可到渠道详情刷新或手动添加。'
          }
        >
          <select
            value={candidateID}
            disabled={loadingCandidates || !candidates.length}
            onChange={e => selectCandidate(e.target.value)}
          >
            {!candidates.length && <option value="">{loadingCandidates ? '正在加载候选模型…' : candidateError ? '候选模型读取失败' : '暂无候选模型'}</option>}
            {candidates.map(c => (
              <option key={c.id} value={c.id}>
                {c.upstream_model}
              </option>
            ))}
          </select>
        </Field>
        </fieldset>
        <fieldset className="formSectionFieldset" disabled={busy}>
        <legend className="formSectionLegend">对外调用名称</legend>
        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>对外名称（alias）</span>
              <small className="charCountHint">{Array.from(alias).length}/128</small>
            </div>
          }
          hint="业务系统调用 /v1/* 时使用的 model 值，发布后在服务端不可改名。"
        >
          <input
            className="mono"
            aria-invalid={!!aliasError}
            aria-describedby={aliasError ? 'publish-alias-error' : undefined}
            value={alias}
            onChange={e => { setAlias(e.target.value); setAliasError(''); setConflict(false); setAppend(false); setError('') }}
            placeholder="例如：campus-gpt"
          />
        </Field>
        {aliasError && <p className="notice warning" role="alert" id="publish-alias-error">{aliasError}</p>}
        </fieldset>
        <details className="formAdvanced">
        <summary>显示名称与描述（可选）</summary>
        <div className="drawerStack">
        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>显示名称（可选）</span>
              <small className="charCountHint">{Array.from(displayName).length}/128</small>
            </div>
          }
          hint="只用于管理台展示，不改变客户端调用名。"
        >
          <input
            maxLength={128}
            disabled={busy}
            value={displayName}
            onChange={e => setDisplayName(e.target.value)}
            placeholder="例如：校园通用大语言模型"
          />
        </Field>

        <Field
          label={
            <div className="fieldLabelWithCount">
              <span>描述（可选）</span>
              <small className="charCountHint">{Array.from(description).length}/1024</small>
            </div>
          }
          hint="简要说明适用场景，不影响运行时行为。"
        >
          <textarea
            rows={3}
            maxLength={1024}
            disabled={busy}
            value={description}
            onChange={e => setDescription(e.target.value)}
            placeholder="面向全校的通用大语言模型，适用于问答、写作、分析等多种场景。"
          />
        </Field>
        </div>
        </details>
        {!loading && !providerError && !providers.length && <div className="notice warning">
          暂无启用渠道。<a href={pathFor('admin', 'providers')} onClick={onClose}>前往上游渠道配置</a>
        </div>}
        {!loadingCandidates && providerID && !candidateError && !candidates.length && <p className="hint">
          <a href={pathFor('admin', 'providers')} onClick={onClose}>前往渠道详情维护候选模型</a>
        </p>}
        {conflict && (
          <div className="checks">
            <label>
              <input type="checkbox" checked={append} onChange={e => setAppend(e.target.checked)} />
              确认把此候选追加为备用线路
            </label>
          </div>
        )}

        {providerError && <div className="notice warning" role="alert">
          {providerError}
          <Button size="sm" onClick={() => setProviderRetry(n => n + 1)}>重试加载渠道</Button>
        </div>}
        {candidateError && <div className="notice warning" role="alert">
          {candidateError}
          <Button size="sm" onClick={() => setCandidateRetry(n => n + 1)}>重试加载候选模型</Button>
        </div>}
        {error && <div className="notice warning" role="alert">{error}</div>}
      </div>
    </Drawer>
  )
}
