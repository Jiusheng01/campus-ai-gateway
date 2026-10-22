import {describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Drawer} from './Drawer'

describe('Drawer keyboard behavior', () => {
  it('keeps keyboard focus inside the drawer and restores the opener after Escape', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const opener = document.createElement('button')
    opener.textContent = '打开详情'
    document.body.appendChild(opener)
    opener.focus()
    const {unmount} = render(
      <Drawer title="模型详情" onClose={onClose} footer={<button>保存</button>}>
        <input aria-label="名称" />
      </Drawer>,
    )
    const close = screen.getByRole('button', {name: '关闭'})
    const save = screen.getByRole('button', {name: '保存'})
    close.focus()
    await user.tab({shift: true})
    expect(save).toHaveFocus()
    await user.tab()
    expect(close).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
    unmount()
    expect(opener).toHaveFocus()
    opener.remove()
  })
  it('keeps the focused field when its parent updates a close callback', async () => {
    const {rerender} = render(<Drawer title="编辑渠道" onClose={() => {}}><input aria-label="名称" /></Drawer>)
    const input = screen.getByRole('textbox', {name: '名称'})
    input.focus()
    rerender(<Drawer title="编辑渠道" onClose={() => {}}><input aria-label="名称" /></Drawer>)
    expect(input).toHaveFocus()
  })
})
