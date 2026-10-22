import {describe, expect, it} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Table} from './Table'

describe('Table keyboard access', () => {
  it('offers a named focus target for horizontally scrolling columns', async () => {
    const user = userEvent.setup()
    render(<Table heads={['模型', '渠道']} minWidth={800}><tr><td>campus-model</td><td>渠道一</td></tr></Table>)
    await user.tab()
    expect(screen.getByRole('region', {name: '数据表格，可横向滚动查看完整列'})).toHaveFocus()
    expect(screen.getByRole('columnheader', {name: '渠道'})).toBeInTheDocument()
  })
})
