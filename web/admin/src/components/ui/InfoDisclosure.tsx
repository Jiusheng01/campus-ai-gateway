import type {ReactNode} from 'react'

/**
 * Collapsible detail block for information a user only needs on demand.
 *
 * Native `<details>` is deliberate: it is keyboard reachable, opens on touch
 * as well as click, and keeps its content in the document for screen readers
 * and find-in-page. Nothing here depends on hover, which a touch device cannot
 * produce.
 */
export function InfoDisclosure({summary, children}: {summary: ReactNode; children: ReactNode}) {
  return (
    <details className="infoDisclosure">
      <summary>{summary}</summary>
      <div className="infoDisclosureBody">{children}</div>
    </details>
  )
}
