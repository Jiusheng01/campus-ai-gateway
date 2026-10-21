import {describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Pagination} from './Pagination'

describe('Pagination', () => {
  it('names the visible range and disables navigation beyond the collection', async () => {
    const user = userEvent.setup()
    const onPageChange = vi.fn()
    render(<Pagination page={1} pageSize={15} total={32} onPageChange={onPageChange} />)
    expect(screen.getByText('1–15 / 共 32 条')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '上一页'})).toBeDisabled()
    await user.click(screen.getByRole('button', {name: '下一页'}))
    expect(onPageChange).toHaveBeenCalledWith(2)
  })
  it('does not invent a total for an API that only exposes a next page', () => {
    const {rerender} = render(<Pagination page={2} pageSize={50} hasNext onPageChange={() => {}} />)
    expect(screen.getByText('第 2 页')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '下一页'})).toBeEnabled()
    rerender(<Pagination page={2} pageSize={50} onPageChange={() => {}} />)
    expect(screen.getByRole('button', {name: '下一页'})).toBeDisabled()
  })
})
