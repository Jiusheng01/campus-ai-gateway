import {useState} from 'react'
import {Route} from 'lucide-react'
import type {ModelConfig} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type ModelDraft = Partial<ModelConfig> & {id?: string}

/**
 * ModelForm edits the public catalog entry and nothing else.
 *
 * The alias is the contract with callers — quotas, key allow-lists and request
 * logs all store it as a string — so it is fixed after creation. Routes are
 * configuration of a different kind: they change how a request reaches an
 * upstream without changing what the caller asked for, and mixing the two in
 * one form is what previously made a model save silently rewrite deployments.
 */
export function ModelForm({
  value,
  onSave,
  onCancel,
}: {
  value: ModelDraft
  onSave: (v: ModelDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<ModelDraft>(() => ({...value}))
  const isNew = !value.id

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      <Field
        label="对外模型名称"
        hint={
          isNew
            ? '业务系统调用 /v1/* API 时使用的 model 名称，例如 campus-smart。创建后不可修改。'
            : '别名创建后不可修改：额度和密钥白名单都按别名记录，改名会让它们失效。'
        }
      >
        <input
          className="mono"
          value={v.alias || ''}
          onChange={e => setV({...v, alias: e.target.value})}
          placeholder="campus-smart"
          disabled={!isNew}
          autoComplete="off"
        />
      </Field>

      <Field label="显示名称" hint="仅用于控制台展示，调用方仍然使用上面的别名。留空时与别名相同。">
        <input
          value={v.display_name || ''}
          onChange={e => setV({...v, display_name: e.target.value})}
          placeholder="校园智能助手"
        />
      </Field>

      <div className="notice info">
        <Route aria-hidden />
        这里只登记对外模型。保存后请在模型卡片的「添加线路」里配置渠道与上游模型，一条线路都没有的模型不会被路由。
      </div>

      <Field label="状态">
        <select value={v.status || 1} onChange={e => setV({...v, status: +e.target.value})}>
          <option value="1">启用</option>
          <option value="2">停用</option>
        </select>
      </Field>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit">
          {isNew ? '创建模型' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
