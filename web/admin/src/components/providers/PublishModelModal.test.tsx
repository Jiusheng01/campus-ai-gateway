import {describe, expect, it, vi, beforeEach} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {PublishModelModal} from './PublishModelModal'
import {ToastProvider} from '../ui/Toast'
import {api} from '../../api'

vi.mock('../../api', async importOriginal => {
  const actual = await importOriginal<typeof import('../../api')>()
  return {...actual, api: vi.fn()}
})

const apiMock = vi.mocked(api)

const candidate = {
  id: 'pm-1', provider_id: 'p-1', upstream_model: 'up-1', source: 'manual' as const, status: 1,
}

function renderModal(onPublished = vi.fn()) {
  render(
    <ToastProvider>
      <PublishModelModal candidate={candidate} onClose={() => {}} onPublished={onPublished} />
    </ToastProvider>,
  )
  return onPublished
}

describe('PublishModelModal', () => {
  beforeEach(() => apiMock.mockReset())

  it('publishes a new alias and reports success', async () => {
    const user = userEvent.setup()
    const onPublished = renderModal()
    apiMock.mockResolvedValue({
      model: {id: 'm-1', alias: 'up-1'}, route: {id: 'r-1'}, created: true, route_created: true, version: 1,
    })
    await user.click(screen.getByRole('button', {name: '发布'}))
    expect(apiMock).toHaveBeenCalledWith('/admin/models/publish', expect.objectContaining({method: 'POST'}))
    const body = JSON.parse(apiMock.mock.calls[0]![1]!.body as string)
    expect(body.append_to_existing).toBe(false)
    expect(onPublished).toHaveBeenCalled()
  })

  it('requires explicit confirmation after an alias conflict', async () => {
    const user = userEvent.setup()
    renderModal()
    apiMock.mockRejectedValueOnce({status: 409, message: 'alias_exists'})
    await user.click(screen.getByRole('button', {name: '发布'}))
    expect(await screen.findByText(/该对外名称已存在/)).toBeInTheDocument()

    apiMock.mockResolvedValueOnce({model: {id: 'm-1', alias: 'up-1'}, route: {id: 'r-2'}, created: false, route_created: true, version: 2})
    await user.click(screen.getByLabelText(/确认把此候选追加为备用线路/))
    await user.click(screen.getByRole('button', {name: '确认发布'}))
    const body = JSON.parse(apiMock.mock.calls[1]![1]!.body as string)
    expect(body.append_to_existing).toBe(true)
  })
})
