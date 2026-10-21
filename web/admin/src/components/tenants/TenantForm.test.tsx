import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {TenantForm} from './TenantForm'
import type {ModelConfig} from '../../api'

const models: ModelConfig[] = [
  {id: 'm-1', alias: 'campus-gpt', display_name: '校园通用助手', description: '', status: 1, config_version: 1},
  {id: 'm-2', alias: 'campus-code', display_name: '代码助手', description: '', status: 1, config_version: 1},
]

const renderForm = (value: Parameters<typeof TenantForm>[0]['value']) => {
  const onSave = vi.fn()
  render(<TenantForm value={value} models={models} onSave={onSave} onCancel={vi.fn()} />)
  return onSave
}

// The Tenant allowlist carries the same three meanings as a key's: "*" is every
// enabled model (and future ones), a named list restricts, [] denies. The form
// has to render each of them and send the same shape back, because an edit that
// silently widened or emptied the scope would change what members may issue.
describe('TenantForm model allowlist', () => {
  it('renders a wildcard Tenant as checked and saves the wildcard unchanged', () => {
    const onSave = renderForm({id: 't-1', code: 'dept', name: 'Dept', status: 1, allowed_models: ['*']})

    expect(screen.getByRole('checkbox', {name: /全部对外模型/})).toBeChecked()
    expect(screen.getByText('已启用通配符授权')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: '保存修改'}))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({allowed_models: ['*']}))
  })

  it('renders an explicit Tenant as chips and saves that list unchanged', () => {
    const onSave = renderForm({id: 't-1', code: 'dept', name: 'Dept', status: 1, allowed_models: ['campus-gpt']})

    expect(screen.getByRole('checkbox', {name: /全部对外模型/})).not.toBeChecked()
    expect(screen.getByText('campus-gpt')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: '保存修改'}))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({allowed_models: ['campus-gpt']}))
  })

  it('renders an empty Tenant as no models and saves the explicit empty list', () => {
    const onSave = renderForm({id: 't-1', code: 'dept', name: 'Dept', status: 1, allowed_models: []})

    expect(screen.getByRole('checkbox', {name: /全部对外模型/})).not.toBeChecked()
    expect(screen.getByText(/未选择任何模型/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: '保存修改'}))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({allowed_models: []}))
  })

  it('starts a new Tenant at the wildcard default and can narrow it to named models', () => {
    const onSave = renderForm({status: 1})

    expect(screen.getByRole('checkbox', {name: /全部对外模型/})).toBeChecked()

    // Turning the wildcard off starts from the empty-list decision, then the
    // dropdown adds one named alias — the same interaction as the key form.
    fireEvent.click(screen.getByRole('checkbox', {name: /全部对外模型/}))
    const picker = document.querySelector('.addModelSelect') as HTMLSelectElement
    fireEvent.change(picker, {target: {value: 'campus-code'}})

    fireEvent.click(screen.getByRole('button', {name: '创建租户'}))
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({status: 1, allowed_models: ['campus-code']}),
    )
  })

  it('turning the wildcard back on replaces any named choices', () => {
    const onSave = renderForm({id: 't-1', status: 1, allowed_models: ['campus-gpt', 'campus-code']})

    fireEvent.click(screen.getByRole('checkbox', {name: /全部对外模型/}))
    fireEvent.click(screen.getByRole('button', {name: '保存修改'}))

    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({allowed_models: ['*']}))
  })
})
