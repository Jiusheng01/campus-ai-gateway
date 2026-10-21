import {Sparkles, X} from 'lucide-react'

/** One selectable Virtual Model. Display name is optional; the alias is the value. */
export type ModelOption = {
  alias: string
  displayName?: string
}

export type ModelScopePickerProps = {
  /**
   * The scope as the API stores it: ["*"], an explicit list, or [] (none).
   * The component never invents a different representation.
   */
  value: string[]
  onChange: (next: string[]) => void
  options: ModelOption[]
  /**
   * Whether the wildcard choice exists. A personal key under an explicit
   * Tenant may only choose named models, so its form passes false and the
   * checkbox disappears instead of offering a scope the server would expand
   * or refuse.
   */
  allowWildcard?: boolean
  title?: string
  wildcardLabel?: string
  wildcardNoticeTitle?: string
  wildcardNoticeText?: string
  emptyPlaceholder?: string
  addPlaceholder?: string
  hint?: string
  /**
   * Asked before the wildcard changes. Returning false leaves the scope as it
   * was; the administrator key form uses this to confirm that narrowing a
   * wildcard really drops every named model.
   */
  confirmWildcardChange?: (on: boolean) => boolean | Promise<boolean>
}

/**
 * The model-scope picker shared by the tenant form, the administrator key form
 * and the personal console's create/edit drawer.
 *
 * The three surfaces enforce different ceilings but present the same three
 * shapes, so the selection logic lives here once:
 *   - "*" means every model, including ones added later;
 *   - a named list restricts;
 *   - [] denies everything.
 *
 * Turning the wildcard on replaces any named choices with ["*"]; turning it
 * off starts from [] because "nothing selected" is the honest starting point
 * for a deliberate restriction. Callers that need a different starting point
 * can seed `value` before the operator interacts.
 */
export function ModelScopePicker({
  value,
  onChange,
  options,
  allowWildcard = true,
  title,
  wildcardLabel = '全部对外模型（含以后新增）',
  wildcardNoticeTitle = '已启用通配符授权',
  wildcardNoticeText = '该范围包含系统内全部已启用的对外模型，以后新增的模型也会自动允许。',
  emptyPlaceholder = '未选择任何模型（将无法调用任何模型）',
  addPlaceholder = '+ 选择要添加的模型…',
  hint,
  confirmWildcardChange,
}: ModelScopePickerProps) {
  const allModels = value.includes('*')
  const selected = value.filter(alias => alias !== '*')
  const byAlias = new Map(options.map(option => [option.alias, option]))

  const applyWildcard = (on: boolean) => onChange(on ? ['*'] : [])
  const changeWildcard = (on: boolean) => {
    if (!confirmWildcardChange) {
      applyWildcard(on)
      return
    }
    const decision = confirmWildcardChange(on)
    // A synchronous yes must update the checkbox in the same event; only a
    // confirmation dialog defers the change.
    if (typeof decision === 'boolean') {
      if (decision) applyWildcard(on)
      return
    }
    void decision.then(ok => {
      if (ok) applyWildcard(on)
    })
  }

  const addModel = (alias: string) => {
    if (!alias || selected.includes(alias)) return
    onChange([...selected, alias])
  }

  const removeModel = (alias: string) => {
    onChange(selected.filter(item => item !== alias))
  }

  return (
    <div className="multiSelectWrapper">
      <div className="multiSelectHead">
        {title && <span className="multiSelectTitle">{title}</span>}
        {allowWildcard && (
          <label className="wildcardToggleLabel">
            <input
              type="checkbox"
              checked={allModels}
              onChange={e => void changeWildcard(e.target.checked)}
            />
            <span>{wildcardLabel}</span>
          </label>
        )}
      </div>

      {allModels ? (
        <div className="wildcardActiveNotice">
          <Sparkles aria-hidden />
          <div>
            <b>{wildcardNoticeTitle}</b>
            <p>{wildcardNoticeText}</p>
          </div>
        </div>
      ) : (
        <>
          <div className="chipsContainer">
            {selected.length ? (
              selected.map(alias => {
                const option = byAlias.get(alias)
                return (
                  <span className="modelSelectChip" key={alias}>
                    <span className="chipAlias mono">{alias}</span>
                    {option?.displayName && <span className="chipName">({option.displayName})</span>}
                    <button
                      type="button"
                      className="chipRemoveButton"
                      onClick={() => removeModel(alias)}
                      title="移除模型"
                    >
                      <X aria-hidden />
                    </button>
                  </span>
                )
              })
            ) : (
              <span className="chipsEmptyPlaceholder">{emptyPlaceholder}</span>
            )}
          </div>

          <div className="addModelDropdownRow">
            <select
              className="addModelSelect"
              value=""
              onChange={e => addModel(e.target.value)}
            >
              <option value="">{addPlaceholder}</option>
              {options
                .filter(option => !selected.includes(option.alias))
                .map(option => (
                  <option key={option.alias} value={option.alias}>
                    {option.alias}
                    {option.displayName ? ` (${option.displayName})` : ''}
                  </option>
                ))}
            </select>
          </div>
        </>
      )}

      {hint && <small className="fieldHint">{hint}</small>}
    </div>
  )
}
