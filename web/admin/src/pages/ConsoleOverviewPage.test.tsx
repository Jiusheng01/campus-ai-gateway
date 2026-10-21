import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import {ConsoleOverviewPage} from './ConsoleOverviewPage'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const base = {
  account_id: 'u-1',
  username: 'member-one',
  display_name: '校园用户',
  roles: ['member'],
  status: 1,
}

function stubMe(overrides: Record<string, unknown>, status = 200) {
  const fetchMock = vi.fn(async () =>
    jsonResponse(
      status >= 400
        ? {error: {...(overrides as object)}}
        : {
            ...base,
            ...overrides,
          },
      status,
    ),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('personal console overview', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the account and an explicit unassigned-Tenant state', async () => {
    stubMe({tenant_id: '', tenant_name: '', workspace_usable: false})
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('member-one')).toBeInTheDocument()
    expect(screen.getByText('校园用户')).toBeInTheDocument()
    expect(screen.getByText('普通用户')).toBeInTheDocument()
    expect(screen.getByText('正常')).toBeInTheDocument()
    expect(screen.getByText('尚未分配租户')).toBeInTheDocument()
    expect(screen.getByText(/请联系管理员在「用户」中为你的账号分配租户/)).toBeInTheDocument()
    expect(screen.queryByText('可用')).not.toBeInTheDocument()
  })

  it('shows the assigned Tenant and a usable workspace', async () => {
    stubMe({tenant_id: 't-1', tenant_name: '校园信息化', workspace_usable: true})
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('校园信息化')).toBeInTheDocument()
    expect(screen.getByText('可用')).toBeInTheDocument()
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
  })

  it('reports a failed read instead of inventing an unassigned state', async () => {
    stubMe({code: 'admin_internal_error', message: 'database unavailable'}, 500)
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
  })
})
