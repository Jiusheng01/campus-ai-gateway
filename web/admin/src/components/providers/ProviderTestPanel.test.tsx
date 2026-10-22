import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {ProviderTestPanel} from './ProviderTestPanel'
import {ToastProvider} from '../ui/Toast'
import type {Provider} from '../../api'

const provider: Provider = {id: 'p1', name: 'alpha', base_url: 'https://example.com', protocol: 'openai-chat', provider_type: '', description: '', credential_env: '', status: 1}
function response(body: unknown, status = 200) {
  return {ok: status < 400, status, text: async () => JSON.stringify(body)} as Response
}
afterEach(() => vi.unstubAllGlobals())

describe('ProviderTestPanel', () => {
  it('keeps an invalid token limit visible and explains the allowed range', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({channel: 'alpha', status_code: 200})))
    render(<ToastProvider><ProviderTestPanel provider={provider} canPreviewContent={false} onTested={() => {}} /></ToastProvider>)
    await userEvent.type(screen.getByLabelText(/上游模型/), 'qa-model')
    await userEvent.clear(screen.getByLabelText('最大输出 Token'))
    await userEvent.type(screen.getByLabelText('最大输出 Token'), '0')
    await userEvent.click(screen.getByRole('button', {name: '开始诊断'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('1 至 1024')
    expect(screen.getByLabelText('最大输出 Token')).toHaveValue(0)
  })

  it('shows a safe diagnostic failure and refreshes the recorded last test on failure too', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({error: {code: 'provider_unavailable', message: 'Upstream connection failed'}}, 502)))
    const tested = vi.fn()
    render(<ToastProvider><ProviderTestPanel provider={provider} canPreviewContent={false} onTested={tested} /></ToastProvider>)
    await userEvent.type(screen.getByLabelText(/上游模型/), 'qa-model')
    await userEvent.click(screen.getByRole('button', {name: '开始诊断'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('Upstream connection failed')
    await waitFor(() => expect(tested).toHaveBeenCalledOnce())
    expect(screen.queryByText('诊断成功')).not.toBeInTheDocument()
    expect(screen.getByLabelText(/上游模型/)).toHaveValue('qa-model')
  })
})
