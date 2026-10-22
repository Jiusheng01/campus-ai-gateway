import type {ReactNode} from 'react'

/**
 * Data table. Horizontally scrollable rather than squeezed: on a wide console
 * the operator wants every column, and on a narrow one a scroll is clearer than
 * a wrapped row.
 */
export function Table({heads, children, minWidth = 780, label = '数据表格，可横向滚动查看完整列'}: {heads: ReactNode[]; children: ReactNode; minWidth?: number; label?: string}) {
  return (
    <div className="tableWrap" tabIndex={0} role="region" aria-label={label}>
      <table className="data" style={{minWidth}}>
        <thead>
          <tr>
            {heads.map((h, i) => (
              <th key={i} scope="col">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}
