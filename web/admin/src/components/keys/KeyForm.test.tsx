import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    allowed_models: ['*'],
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

    // Enter the name and save
    const nameInput = screen.getByPlaceholderText('请输入 API Key 名称')
    fireEvent.change(nameInput, {target: {value: '教务测试 Key'}})
    fireEvent.click(screen.getByRole('button', {name: '创建 API Key'}))

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

describe('KeyForm quota preview', () => {
  const tenantWith = (limits: Partial<Tenant>): Tenant => ({
    ...mockTenants[0],
    daily_token_limit: undefined as unknown as number,
    monthly_request_limit: undefined as unknown as number,
    rpm_limit: undefined as unknown as number,
    ...limits,
  })

  const renderForm = (tenant: Tenant) =>
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '', tenant_id: tenant.id, allowed_models: ['campus-gpt']}}
            tenants={[tenant]}
            models={mockModels}
            onSave={vi.fn()}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

  // The three states must stay distinguishable: a zero limit is a deliberate
  // "no cap" (无限制), while a missing field means the tenant document predates
  // the limit (未设置). Collapsing them into one label promises a cap that does
  // not exist, or hides one that does.
  it('renders a configured limit as its value', () => {
    renderForm(tenantWith({daily_token_limit: 2000000, monthly_request_limit: 80000, rpm_limit: 600}))

    const card = screen.getByText('租户配额预览').closest('.tenantQuotaPreviewCard') as HTMLElement
    expect(within(card).getByText('200万')).toBeInTheDocument()
    expect(within(card).getByText('8万')).toBeInTheDocument()
    expect(within(card).getByText('600 / 分钟')).toBeInTheDocument()
    expect(within(card).queryByText('无限制')).not.toBeInTheDocument()
    expect(within(card).queryByText('未设置')).not.toBeInTheDocument()
  })

  it('renders a zero limit as 无限制', () => {
    renderForm(tenantWith({daily_token_limit: 0, monthly_request_limit: 0, rpm_limit: 0}))

    const card = screen.getByText('租户配额预览').closest('.tenantQuotaPreviewCard') as HTMLElement
    // One per limit: daily tokens, monthly requests, RPM.
    expect(within(card).getAllByText('无限制')).toHaveLength(3)
    expect(within(card).queryByText('未设置')).not.toBeInTheDocument()
  })

  it('renders an absent limit as 未设置, not as 无限制', () => {
    renderForm(tenantWith({}))

    const card = screen.getByText('租户配额预览').closest('.tenantQuotaPreviewCard') as HTMLElement
    expect(within(card).getAllByText('未设置')).toHaveLength(3)
    expect(within(card).queryByText('无限制')).not.toBeInTheDocument()
  })

  it('mixes the three states when the tenant carries different limits', () => {
    renderForm(tenantWith({daily_token_limit: 50000, monthly_request_limit: 0, rpm_limit: undefined}))

    const card = screen.getByText('租户配额预览').closest('.tenantQuotaPreviewCard') as HTMLElement
    const values = [...card.querySelectorAll('.quotaMetricValue')].map(el => el.textContent)
    expect(values).toEqual(['5万', '无限制', '未设置'])
  })

  it('uses API Key vocabulary and keeps decision-record numbers out of operator copy', () => {
    renderForm(mockTenants[0])

    expect(screen.queryByText(/密钥/)).not.toBeInTheDocument()
    expect(screen.queryByText(/ADR-\d+/)).not.toBeInTheDocument()
    expect(screen.getAllByText(/API Key/).length).toBeGreaterThan(0)
  })
})

describe('KeyForm step indicator', () => {
  const activeStepLabel = () => {
    const active = document.querySelector('.formStep.active')
    return active?.textContent?.replace(/^\d/, '') ?? ''
  }

  it('marks the step containing the focused control as the only active one', async () => {
    const user = userEvent.setup()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '', tenant_id: 't-1', allowed_models: ['campus-gpt']}}
            tenants={mockTenants}
            models={mockModels}
            onSave={vi.fn()}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

    // Nothing is focused yet: the first section is the operator's starting point.
    expect(document.querySelectorAll('.formStep.active')).toHaveLength(1)
    expect(activeStepLabel()).toBe('基础信息')

    await user.click(screen.getByPlaceholderText('请输入 API Key 名称'))
    expect(activeStepLabel()).toBe('基础信息')

    await user.click(screen.getByRole('checkbox', {name: /全部对外模型/}))
    expect(activeStepLabel()).toBe('模型授权')

    await user.click(screen.getByPlaceholderText(/192\.168\.1\.0\/24/))
    expect(activeStepLabel()).toBe('网络与安全边界')
  })

  it('advances when focus arrives through keyboard navigation', async () => {
    const user = userEvent.setup()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '', tenant_id: 't-1', allowed_models: ['campus-gpt']}}
            tenants={mockTenants}
            models={mockModels}
            onSave={vi.fn()}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

    screen.getByPlaceholderText('请输入 API Key 名称').focus()
    expect(activeStepLabel()).toBe('基础信息')

    // The tenant select is the next tab stop inside the first section.
    await user.tab()
    expect(activeStepLabel()).toBe('基础信息')

    // One more tab reaches the wildcard toggle in the second section.
    await user.tab()
    expect(activeStepLabel()).toBe('模型授权')
  })

  it('keeps the last section active when focus leaves the form', async () => {
    const user = userEvent.setup()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <KeyForm
            value={{name: '', tenant_id: 't-1', allowed_models: ['campus-gpt']}}
            tenants={mockTenants}
            models={mockModels}
            onSave={vi.fn()}
            onCancel={vi.fn()}
          />
        </ConfirmProvider>
      </ToastProvider>,
    )

    await user.click(screen.getByPlaceholderText(/192\.168\.1\.0\/24/))
    expect(activeStepLabel()).toBe('网络与安全边界')

    // Focus moving out of the form entirely (e.g. to the drawer's close button)
    // must not make the bar claim a different section than the operator chose,
    // nor leave it blank once nothing inside the form holds focus.
    await user.click(screen.getByRole('button', {name: '取消'}))
    expect(activeStepLabel()).toBe('网络与安全边界')
  })
})

describe('SecretModal vocabulary', () => {
  it('calls the credential an API Key everywhere the operator can read it', () => {
    render(
      <ToastProvider>
        <ConfirmProvider>
          <SecretModal value="sk-campus-ai-secret-12345" onClose={vi.fn()} />
        </ConfirmProvider>
      </ToastProvider>,
    )

    expect(screen.getByRole('heading', {name: /API Key/})).toBeInTheDocument()
    expect(screen.queryByText(/密钥/)).not.toBeInTheDocument()
    expect(screen.queryByText(/凭据/)).not.toBeInTheDocument()
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
