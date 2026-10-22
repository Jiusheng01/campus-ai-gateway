import {describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {CopyButton} from './CopyButton'
import {ToastProvider} from './Toast'

describe('CopyButton', () => {
  it('copies the complete identifier and confirms success', async () => {
    const user = userEvent.setup()
    const value = 'campus/research-model-with-a-complete-long-alias'
    render(<ToastProvider><CopyButton value={value} label="复制模型 ID" /></ToastProvider>)
    await user.click(screen.getByRole('button', {name: '复制模型 ID'}))
    expect(await navigator.clipboard.readText()).toBe(value)
    expect(await screen.findByText('已复制到剪贴板')).toBeInTheDocument()
  })
  it('explains how to copy manually when the browser refuses clipboard access', async () => {
    const user = userEvent.setup()
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('denied'))
    render(<ToastProvider><CopyButton value="request-01" /></ToastProvider>)
    await user.click(screen.getByRole('button', {name: '复制'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('复制失败，请选择完整内容后手动复制')
    expect(screen.queryByText('已复制到剪贴板')).not.toBeInTheDocument()
  })
})
