import {useState} from 'react'
import {Boxes} from 'lucide-react'
import {api, type ProviderModel, type PublishResult} from '../../api'
import {Modal} from '../ui/Modal'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {useToast} from '../ui/Toast'

/**
 * Publishing creates the external alias and its first route. It never grants
 * the model to an API Key: that is a separate authorization decision.
 */
export function PublishModelModal({
  candidate,
  onClose,
  onPublished,
}: {
  candidate: ProviderModel
  onClose: () => void
  onPublished: () => void
}) {
  const toast = useToast()
  const [alias, setAlias] = useState(candidate.upstream_model)
  const [displayName, setDisplayName] = useState('')
  const [description, setDescription] = useState('')
  const [append, setAppend] = useState(false)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [error, setError] = useState('')

  const submit = async () => {
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
      title={`发布候选模型：${candidate.upstream_model}`}
      subtitle="对外模型是业务系统调用的稳定名称；发布不会自动授权给任何 API Key。"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" loading={busy} disabled={!alias.trim()} onClick={() => void submit()}>
            {conflict ? '确认发布' : '发布'}
          </Button>
        </>
      }
    >
      <div className="form">
        <Notice>
          <Boxes aria-hidden />
          首条线路使用该候选的上游模型；之后可在「路由策略」中追加备用或加权线路。
        </Notice>
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
