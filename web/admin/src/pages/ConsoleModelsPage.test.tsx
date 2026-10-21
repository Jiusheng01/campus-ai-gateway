import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {ConsoleModelsPage} from './ConsoleModelsPage'
import {ToastProvider} from '../components/ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function stubCatalog(body: unknown, status = 200) {
  const fetchMock = vi.fn(async () => jsonResponse(body, status))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const catalog = {
  models: [
    {alias: 'campus-gpt', display_name: '校园 GPT', description: '通用对话模型', available: true},
    {alias: 'campus-code', display_name: '校园 Code', description: '代码助手', available: true},
  ],
}

function renderPage() {
  return render(
    <ToastProvider>
      <ConsoleModelsPage />
    </ToastProvider>,
  )
}

describe('personal model catalog', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the models, the origin-derived base URL and a copyable example', async () => {
    stubCatalog(catalog)
    renderPage()

    expect(await screen.findByText('校园 GPT')).toBeInTheDocument()
    const table = screen.getByRole('table')
    expect(within(table).getByText('campus-gpt')).toBeInTheDocument()
    expect(within(table).getByText('校园 Code')).toBeInTheDocument()
    expect(within(table).getByText('代码助手')).toBeInTheDocument()

    const baseUrl = `${window.location.origin}/v1`
    expect(screen.getByText(baseUrl)).toBeInTheDocument()

    const picker = screen.getByLabelText('请求示例使用的模型')
    expect(picker).toHaveValue('campus-gpt')
    expect(screen.getByText('请求示例')).toBeInTheDocument()
    expect(screen.getByText('SDK 配置（Python）')).toBeInTheDocument()

    fireEvent.change(picker, {target: {value: 'campus-code'}})
    expect(picker).toHaveValue('campus-code')
    await waitFor(() => {
      expect(
        screen.getAllByText((_, el) => el?.tagName === 'PRE' && !!el.textContent?.includes('campus-code')).length,
      ).toBeGreaterThan(0)
    })
  })

  it('copies the request example with the selected model', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: {writeText},
      configurable: true,
    })
    stubCatalog(catalog)
    renderPage()

    fireEvent.click(await screen.findByRole('button', {name: '复制示例'}))
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = writeText.mock.calls[0][0] as string
    expect(copied).toContain(`curl ${window.location.origin}/v1/chat/completions`)
    expect(copied).toContain('"model": "campus-gpt"')
  })

  it('explains an unassigned account', async () => {
    stubCatalog({models: [], empty_reason: 'no_tenant'})
    renderPage()

    expect(await screen.findByText('尚未分配租户')).toBeInTheDocument()
    expect(screen.getByText(/请联系管理员在「用户」中为你的账号分配租户/)).toBeInTheDocument()
    expect(screen.queryByText('当前租户未开放任何模型')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无可调用的模型')).not.toBeInTheDocument()
  })

  it('explains a Tenant that allows no models', async () => {
    stubCatalog({models: [], empty_reason: 'tenant_allows_none'})
    renderPage()

    expect(await screen.findByText('当前租户未开放任何模型')).toBeInTheDocument()
    expect(screen.getByText(/请联系管理员在租户设置中开放所需模型/)).toBeInTheDocument()
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无可调用的模型')).not.toBeInTheDocument()
  })

  it('explains an empty deployment without pretending the Tenant denied access', async () => {
    stubCatalog({models: [], empty_reason: 'no_eligible_models'})
    renderPage()

    expect(await screen.findByText('暂无可调用的模型')).toBeInTheDocument()
    expect(screen.getByText(/网关当前尚未配置可用的模型线路/)).toBeInTheDocument()
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
    expect(screen.queryByText('当前租户未开放任何模型')).not.toBeInTheDocument()
  })

  it('reports a failed read instead of inventing an empty catalog', async () => {
    stubCatalog({code: 'admin_internal_error', message: 'database unavailable'}, 500)
    renderPage()

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('暂无可调用的模型')).not.toBeInTheDocument()
  })
})
