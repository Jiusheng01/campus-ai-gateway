import {describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Modal} from './Modal'

describe('Modal keyboard behaviour', () => {
  it('cycles Tab and Shift+Tab inside the dialog instead of escaping it', async () => {
    const user = userEvent.setup()
    render(
      <Modal
        title="确认操作"
        onClose={() => {}}
        footer={
          <>
            <button type="button">取消</button>
            <button type="button">确认</button>
          </>
        }
      >
        <p>正文</p>
      </Modal>,
    )

    const panel = screen.getByRole('dialog')
    await waitFor(() => expect(panel).toHaveFocus())

    const close = screen.getByRole('button', {name: '关闭'})
    const last = screen.getByRole('button', {name: '确认'})

    // Tab moves into the dialog's first control…
    await user.tab()
    expect(close).toHaveFocus()

    // …Shift+Tab from the first control wraps to the last…
    await user.tab({shift: true})
    expect(last).toHaveFocus()

    // …and Tab from the last wraps back to the first, so focus never leaves.
    await user.tab()
    expect(close).toHaveFocus()
  })

  it('closes on Escape and restores the previously focused element', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const opener = document.createElement('button')
    opener.textContent = '打开'
    document.body.appendChild(opener)
    opener.focus()

    const {unmount} = render(
      <Modal title="确认操作" onClose={onClose}>
        <p>正文</p>
      </Modal>,
    )
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveFocus())
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)

    unmount()
    expect(opener).toHaveFocus()
    opener.remove()
  })
})
