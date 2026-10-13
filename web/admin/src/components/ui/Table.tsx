import type {ReactNode} from 'react'

/**
 * Data table. Horizontally scrollable rather than squeezed: on a wide console
 * the operator wants every column, and on a narrow one a scroll is clearer than
 * a wrapped row.
 */
export function Table({heads, children, minWidth = 780}: {heads: ReactNode[]; children: ReactNode; minWidth?: number}) {
  return (
    <div className="tableWrap">
      <table className="data" style={{minWidth}}>
        <thead>
          <tr>
            {heads.map((h, i) => (
              <th key={i}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}
