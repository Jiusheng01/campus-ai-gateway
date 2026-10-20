import {describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {ProviderForm} from './ProviderForm'

describe('ProviderForm', () => {
  it('requires a credential for a new inline channel and sends only the selected source', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<ProviderForm value={{status: 1, protocol: 'openai-chat'}} onSave={onSave} onCancel={() => {}} />)

    await user.click(screen.getByRole('button', {name: '创建渠道'}))
    expect(screen.getByRole('alert')).toHaveTextContent('请粘贴厂商 API Key')

    await user.type(screen.getByLabelText(/渠道名称/), 'qwen-dev')
    await user.type(screen.getByLabelText(/Base URL/), 'https://dashscope.example/v1')
    await user.type(screen.getByLabelText(/API Key/), 'sk-secret')
    await user.click(screen.getByRole('button', {name: '创建渠道'}))

    expect(onSave).toHaveBeenCalledTimes(1)
    const payload = onSave.mock.calls[0][0]
    expect(payload.name).toBe('qwen-dev')
    expect(payload.credential_secret).toBe('sk-secret')
    expect(payload.credential_env).toBeUndefined()
  })

  it('requires an environment-variable name when env mode is selected', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<ProviderForm value={{status: 1, protocol: 'openai-chat'}} onSave={onSave} onCancel={() => {}} />)

    await user.click(screen.getByLabelText(/改为引用环境变量/))
    await user.type(screen.getByLabelText(/渠道名称/), 'qwen-env')
    await user.type(screen.getByLabelText(/Base URL/), 'https://dashscope.example/v1')
    await user.click(screen.getByRole('button', {name: '创建渠道'}))

    expect(screen.getByRole('alert')).toHaveTextContent('请输入凭据环境变量名')
    expect(onSave).not.toHaveBeenCalled()
  })
})
