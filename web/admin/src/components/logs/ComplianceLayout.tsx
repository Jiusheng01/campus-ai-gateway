import type {ReactNode} from 'react'
import {Search, X} from 'lucide-react'
import {Button} from '../ui/Button'
import {Panel} from '../ui/Panel'

/**
 * The parts of a Compliance Record list that the audit and security pages
 * share, without sharing their business fields.
 *
 * Both endpoints take only a `limit` and answer the newest N rows. There is no
 * server-side filter, pagination or export, so everything here narrows the
 * loaded window in the browser — and says so, because a reader who believed a
 * filter searched the whole table would draw the wrong conclusion from an
 * empty result.
 */
export const RECORD_WINDOW_LIMIT = 200

export function RecordWindowNote({loaded, filtered, narrowed}: {loaded: number; filtered: number; narrowed: boolean}) {
  return (
    <p className="filterNote">
      {narrowed
        ? `筛选后 ${filtered} 条（共读取 ${loaded} 条）`
        : `${loaded} 条记录`}
      {' '}· 接口没有服务端筛选或分页，筛选只作用于这最近 {RECORD_WINDOW_LIMIT} 条。
    </p>
  )
}

export function RecordFilters({
  search,
  onSearch,
  searchPlaceholder,
  children,
  onReset,
}: {
  search: string
  onSearch: (v: string) => void
  searchPlaceholder: string
  children?: ReactNode
  onReset: () => void
}) {
  return (
    <div className="filterCard">
      <div className="filtersBar">
        <div className="searchFieldGroup">
          <Search className="searchFieldIcon" aria-hidden />
          <input
            className="filterSearchInput"
            placeholder={searchPlaceholder}
            value={search}
            onChange={e => onSearch(e.target.value)}
          />
        </div>
        {children}
        <div className="filterActionButtons">
          <Button size="sm" icon={<X />} onClick={onReset}>重置</Button>
        </div>
      </div>
    </div>
  )
}

/**
 * A record list.
 *
 * `source` is which of the three things the page knows about the read: it is
 * loading, it failed, or it answered with nothing. They render differently on
 * purpose — a failed read is not an empty table — and only a successful,
 * non-empty read can be narrowed, which is when the filtered-empty state
 * applies.
 */
export function RecordList({
  title,
  sub,
  filters,
  source,
  isFilteredEmpty,
  emptySource,
  emptyFiltered,
  children,
  note,
}: {
  title: string
  sub: string
  filters: ReactNode
  source: 'loading' | 'error' | 'empty' | 'ready'
  isFilteredEmpty: boolean
  emptySource: ReactNode
  emptyFiltered: ReactNode
  children: ReactNode
  note?: ReactNode
}) {
  return (
    <Panel title={title} sub={sub}>
      {filters}
      {source !== 'ready' ? emptySource : isFilteredEmpty ? emptyFiltered : children}
      {note}
    </Panel>
  )
}

export function FilterField({label, children}: {label: string; children: ReactNode}) {
  return (
    <label className="filterFieldInline">
      <span className="filterLabel">{label}</span>
      {children}
    </label>
  )
}
