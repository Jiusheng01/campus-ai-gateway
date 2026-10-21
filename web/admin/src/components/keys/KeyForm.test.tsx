import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {KeyForm} from './KeyForm'
import {SecretModal} from './SecretModal'
import {ConfirmProvider} from '../ui/ConfirmDialog'
import {ToastProvider} from '../ui/Toast'
import type {ModelConfig, Tenant} from '../../api'

const mockTenants: Tenant[] = [
  {
    id: 't-1',
    code: 'cs-dept',
    name: '计算机学院',
    status: 1,
    daily_token_limit: 1000000,
    monthly_token_limit: 30000000,
    monthly_request_limit: 50000,
    monthly_budget_micros: 0,
    rpm_limit: 600,
    tpm_limit: 100000,
    max_concurrency: 50,
  },
]

const mockModels: ModelConfig[] = [
  {
    id: 'm-1',
    alias: 'campus-gpt',
    display_name: '校园通用助手',
    description: '通用对话模型',
    status: 1,
    config_version: 1,
  },
  {
    id: 'm-2',
    alias: 'campus-code',
    display_name: '代码助手',
    description: '代码生成模型',
    status: 1,
    config_version: 1,
  },
]

describe('KeyForm', () => {
  it('renders tenant quota preview and supports adding and removing models', () => {
    const onSave = vi.fn()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '', tenant_id: 't-1', allowed_models: ['campus-gpt']}}
            tenants={mockTenants}
            models={mockModels}
            onSave={onSave}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

    // Verify tenant quota preview is rendered
    expect(screen.getByText('租户配额预览')).toBeInTheDocument()
    expect(screen.getByText('100万')).toBeInTheDocument()
    expect(screen.getByText('5万')).toBeInTheDocument()

    // Verify model chip is rendered
    expect(screen.getByText('campus-gpt')).toBeInTheDocument()

    // Enter Key name and save
    const nameInput = screen.getByPlaceholderText('请输入 Key 名称')
    fireEvent.change(nameInput, {target: {value: '教务测试 Key'}})
    fireEvent.click(screen.getByRole('button', {name: '创建 Key'}))

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        name: '教务测试 Key',
        tenant_id: 't-1',
        allowed_models: ['campus-gpt'],
      }),
    )
  })

  it('handles wildcard model authorization toggle', () => {
    const onSave = vi.fn()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '全局 Key', tenant_id: 't-1', allowed_models: []}}
            tenants={mockTenants}
            models={mockModels}
            onSave={onSave}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

    const wildcardCheckbox = screen.getByRole('checkbox', {name: /全部对外模型/})
    expect(wildcardCheckbox).not.toBeChecked()

    // Turn wildcard on
    fireEvent.click(wildcardCheckbox)
    expect(wildcardCheckbox).toBeChecked()
    expect(screen.getByText('已启用通配符授权')).toBeInTheDocument()
  })
})

describe('SecretModal', () => {
  it('renders the secret key and enforces acknowledgement before completing', () => {
    const onClose = vi.fn()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <SecretModal value="sk-campus-ai-secret-12345" onClose={onClose} />
        </ConfirmProvider>
      </ToastProvider>,
    )

    // Key is rendered
    expect(screen.getByText('sk-campus-ai-secret-12345')).toBeInTheDocument()

    // Complete button is initially disabled
    const completeBtn = screen.getByRole('button', {name: '完成并关闭'})
    expect(completeBtn).toBeDisabled()

    // Tick acknowledgement
    const ackCheckbox = screen.getByRole('checkbox', {name: /我已确认复制并安全妥善保存了此 API Key/})
    fireEvent.click(ackCheckbox)
    expect(completeBtn).toBeEnabled()

    // Click complete
    fireEvent.click(completeBtn)
    expect(onClose).toHaveBeenCalled()
  })
})
