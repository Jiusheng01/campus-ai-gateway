import {describe, expect, it, vi, beforeEach} from 'vitest'
import {render, screen} from '@testing-library/react'
import {ProviderModelsPanel} from './ProviderModelsPanel'
import {ToastProvider} from '../ui/Toast'
import {ConfirmProvider} from '../ui/ConfirmDialog'
import {api} from '../../api'

vi.mock('../../api', () => ({api: vi.fn()}))

const apiMock = vi.mocked(api)

function renderPanel() {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <ProviderModelsPanel providerID="provider-1" />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

describe('ProviderModelsPanel', () => {
  beforeEach(() => apiMock.mockReset())

  it('shows the route/model links derived from the candidate', async () => {
    apiMock.mockResolvedValue({
      data: [{
        id: 'pm-1', provider_id: 'provider-1', upstream_model: 'up-linked',
        source: 'manual', status: 1,
        links: [{route_id: 'r-1', model_id: 'm-1', alias: 'linked-alias', route_status: 1}],
      }],
    })
    renderPanel()
    expect(await screen.findByText('up-linked')).toBeInTheDocument()
    expect(screen.getByText('linked-alias')).toBeInTheDocument()
    expect(screen.getByText('手动添加')).toBeInTheDocument()
  })

  it('renders an empty candidate state without inventing rows', async () => {
    apiMock.mockResolvedValue({data: []})
    renderPanel()
    expect(await screen.findByText('暂无候选模型')).toBeInTheDocument()
  })
})
