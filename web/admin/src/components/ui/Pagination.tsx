import {ChevronLeft, ChevronRight} from 'lucide-react'
import {Button} from './Button'

type Props = {
  page: number
  pageSize: number
  total?: number
  /** Use a known next-page observation when the API does not expose totals. */
  hasNext?: boolean
  loading?: boolean
  onPageChange: (page: number) => void
}

export function Pagination({page, pageSize, total, hasNext = false, loading = false, onPageChange}: Props) {
  const next = total === undefined ? hasNext : page * pageSize < total
  const label = total === undefined
    ? `第 ${page} 页`
    : total === 0 ? '共 0 条' : `${Math.min((page - 1) * pageSize + 1, total)}–${Math.min(page * pageSize, total)} / 共 ${total} 条`
  return (
    <nav className="pagination" aria-label="列表分页">
      <span className="muted" aria-live="polite">{label}</span>
      <div className="paginationActions">
        <Button size="sm" icon={<ChevronLeft />} disabled={loading || page <= 1} onClick={() => onPageChange(page - 1)}>上一页</Button>
        <Button size="sm" icon={<ChevronRight />} disabled={loading || !next} onClick={() => onPageChange(page + 1)}>下一页</Button>
      </div>
    </nav>
  )
}
