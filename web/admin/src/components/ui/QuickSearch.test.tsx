import {afterEach, describe, expect, it, vi} from 'vitest'
import {useState} from 'react'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QuickSearchModal} from './QuickSearch'
import {ToastProvider} from './Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

type SearchOptions = {
  roles?: string[]
  models?: unknown[]
  providers?: unknown[]
  onNavigate?: ReturnType<typeof vi.fn>
  onClose?: ReturnType<typeof vi.fn>
}

function renderSearch({
  roles = ['super_admin'],
  models = [],
  providers = [],
  onNavigate = vi.fn(),
  onClose = vi.fn(),
}: SearchOptions = {}) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    // Both catalogs are the same endpoints the model and channel pages read.
    if (url.startsWith('/admin/models')) return jsonResponse({data: models})
    if (url.startsWith('/admin/providers')) return jsonResponse({data: providers})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  render(
    <ToastProvider>
      <QuickSearchModal roles={roles} onNavigate={onNavigate} onClose={onClose} />
    </ToastProvider>,
  )
  return {onNavigate, onClose, fetchMock}
}

describe('QuickSearchModal', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('moves focus into the search input and exposes the combobox relationship', async () => {
    renderSearch()
    const input = screen.getByRole('combobox')
    await waitFor(() => expect(input).toHaveFocus())
    const list = screen.getByRole('listbox')
    expect(input).toHaveAttribute('aria-expanded', 'true')
    expect(input).toHaveAttribute('aria-autocomplete', 'list')
    expect(input).toHaveAttribute('aria-controls', list.id)
    const first = screen.getAllByRole('option')[0]
    expect(first).toHaveAttribute('aria-selected', 'true')
    expect(input).toHaveAttribute('aria-activedescendant', first.id)
  })

  it('runs the highlighted entry on ArrowDown then Enter', async () => {
    const user = userEvent.setup()
    const {onNavigate, onClose} = renderSearch()
    const input = screen.getByRole('combobox')
    await waitFor(() => expect(input).toHaveFocus())

    // Narrow to the two 安全 entries: 安全事件 first, 安全与访问 second.
    fireEvent.change(input, {target: {value: '安全'}})
    expect(screen.getByRole('option', {name: /安全事件/})).toHaveAttribute('aria-selected', 'true')

    await user.keyboard('{ArrowDown}')
    const access = screen.getByRole('option', {name: /安全与访问/})
    expect(access).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('option', {name: /安全事件/})).toHaveAttribute('aria-selected', 'false')
    expect(input).toHaveAttribute('aria-activedescendant', access.id)

    await user.keyboard('{Enter}')
    expect(onNavigate).toHaveBeenCalledWith('access')
    expect(onClose).toHaveBeenCalled()
  })

  it('closes on Escape', async () => {
    const user = userEvent.setup()
    const {onClose} = renderSearch()
    await waitFor(() => expect(screen.getByRole('combobox')).toHaveFocus())
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalled()
  })

  it('offers no catalog results or dead actions to roles that cannot reach those pages', () => {
    const {fetchMock} = renderSearch({roles: ['member']})
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.queryByText('上游渠道')).not.toBeInTheDocument()
    expect(screen.queryByText('对外模型')).not.toBeInTheDocument()
    expect(screen.queryByText('模型连通测试')).not.toBeInTheDocument()
    expect(screen.queryByText('创建 API Key')).not.toBeInTheDocument()
    expect(screen.queryByText('新增上游渠道')).not.toBeInTheDocument()
    // The one page a member may enter is still offered.
    expect(screen.getByRole('option', {name: /安全与访问/})).toBeInTheDocument()
  })

  it('searches the catalogs and offers their rows to roles that may open those pages', async () => {
    const {fetchMock} = renderSearch({
      roles: ['super_admin'],
      models: [{id: 'm-1', alias: 'campus-gpt', display_name: '校园助手', status: 1, config_version: 2}],
      providers: [{id: 'p-1', name: 'qwen', provider_type: 'qwen', base_url: 'https://qwen.example', status: 1}],
    })
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith('/admin/models', expect.anything())
      expect(fetchMock).toHaveBeenCalledWith('/admin/providers', expect.anything())
    })

    const input = screen.getByRole('combobox')
    fireEvent.change(input, {target: {value: 'qwen'}})
    expect(await screen.findByRole('option', {name: /qwen/})).toBeInTheDocument()
  })

  it('returns focus to the control that opened it', async () => {
    const user = userEvent.setup()

    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <ToastProvider>
          <button type="button" onClick={() => setOpen(true)}>
            打开搜索
          </button>
          {open && (
            <QuickSearchModal roles={['super_admin']} onNavigate={() => {}} onClose={() => setOpen(false)} />
          )}
        </ToastProvider>
      )
    }

    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    render(<Harness />)
    const trigger = screen.getByRole('button', {name: '打开搜索'})
    trigger.focus()
    await user.click(trigger)
    await waitFor(() => expect(screen.getByRole('combobox')).toHaveFocus())
    await user.keyboard('{Escape}')
    await waitFor(() => expect(trigger).toHaveFocus())
  })
})
