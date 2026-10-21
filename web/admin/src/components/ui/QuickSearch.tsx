import {useEffect, useId, useMemo, useRef, useState, type KeyboardEvent} from 'react'
import {Boxes, Network, Search, X, type LucideIcon} from 'lucide-react'
import {api, type ModelConfig, type Provider} from '../../api'
import {allNavItems, allowedPages, pageMeta, type Page} from '../../app/navigation'
import {Modal} from './Modal'

export type QuickSearchItem = {
  id: string
  category: '导航' | '对外模型' | '上游渠道'
  title: string
  subtitle?: string
  icon: LucideIcon
  action: () => void
}

type Props = {
  onClose: () => void
  onNavigate: (page: Page) => void
  roles?: string[]
}

/**
 * Global quick search.
 *
 * It renders the console's Modal primitive rather than a lookalike, so focus
 * capture, focus restore and Escape have one implementation. The result list is
 * a combobox: the input owns the keyboard, and the highlighted row is announced
 * through aria-activedescendant.
 *
 * Mounting is controlled by the shell, so opening it starts from empty state;
 * there is no `open` flag whose reset effect could race the first keystroke.
 */
export function QuickSearchModal({onClose, onNavigate, roles = []}: Props) {
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [models, setModels] = useState<ModelConfig[]>([])
  const [providers, setProviders] = useState<Provider[]>([])
  const inputRef = useRef<HTMLInputElement>(null)
  const baseId = useId()
  const listId = `${baseId}-list`
  const optionId = (index: number) => `${baseId}-option-${index}`

  // One source of truth for who may enter which page: the navigation itself.
  // A search result that navigates to a page the account cannot open is the
  // same dead end as a nav item that 403s.
  const pages = useMemo(() => allowedPages(roles), [roles])
  const canSearchModels = pages.includes('models')
  const canSearchProviders = pages.includes('providers')

  // Modal focuses its own panel from a parent effect, and React flushes child
  // effects first. Deferring one tick is what puts the caret in the field the
  // operator is about to type into, instead of back on the dialog panel.
  useEffect(() => {
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0)
    return () => window.clearTimeout(timer)
  }, [])

  // Catalogs are fetched only for roles the gateway lets onto those pages. The
  // fetch is pointless otherwise: /admin/models and /admin/providers answer 403.
  useEffect(() => {
    if (canSearchModels) {
      void api<{data: ModelConfig[]}>('/admin/models')
        .then(d => setModels(d.data || []))
        .catch(() => setModels([]))
    } else {
      setModels([])
    }
    if (canSearchProviders) {
      void api<{data: Provider[]}>('/admin/providers')
        .then(d => setProviders(d.data || []))
        .catch(() => setProviders([]))
    } else {
      setProviders([])
    }
  }, [canSearchModels, canSearchProviders])

  const navItems: QuickSearchItem[] = useMemo(
    () =>
      allNavItems
        .filter(item => pages.includes(item.id))
        .map(item => ({
          id: `nav-${item.id}`,
          category: '导航',
          title: item.label,
          subtitle: pageMeta[item.id].subtitle,
          icon: item.icon,
          action: () => {
            onNavigate(item.id)
            onClose()
          },
        })),
    [pages, onNavigate, onClose],
  )

  const modelItems: QuickSearchItem[] = useMemo(
    () =>
      models.map(m => ({
        id: `model-${m.id}`,
        category: '对外模型',
        title: m.alias,
        subtitle: `${m.display_name || '未设置显示名'} · ${m.status === 1 ? '已启用' : '已停用'} · 版本 v${m.config_version}`,
        icon: Boxes,
        action: () => {
          onNavigate('models')
          onClose()
        },
      })),
    [models, onNavigate, onClose],
  )

  const providerItems: QuickSearchItem[] = useMemo(
    () =>
      providers.map(p => ({
        id: `provider-${p.id}`,
        category: '上游渠道',
        title: p.name,
        subtitle: `${p.provider_type || '自定义'} · ${p.base_url} · ${p.status === 1 ? '在线' : '停用'}`,
        icon: Network,
        action: () => {
          onNavigate('providers')
          onClose()
        },
      })),
    [providers, onNavigate, onClose],
  )

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    const all = [...navItems, ...modelItems, ...providerItems]
    if (!q) return all.slice(0, 10)
    return all
      .filter(
        item =>
          item.title.toLowerCase().includes(q) ||
          (item.subtitle && item.subtitle.toLowerCase().includes(q)) ||
          item.category.toLowerCase().includes(q),
      )
      .slice(0, 12)
  }, [query, navItems, modelItems, providerItems])

  // Clamp instead of storing a second copy of the list: catalog rows arriving
  // or a narrower query must never leave the highlight past the last row.
  const activeItem = filteredItems.length
    ? filteredItems[Math.min(activeIndex, filteredItems.length - 1)]
    : undefined
  const activePosition = activeItem ? filteredItems.indexOf(activeItem) : -1

  // A new query is a new result set; the first row is the sensible starting
  // point and keeps the announced option in sync with what is on screen.
  useEffect(() => {
    setActiveIndex(0)
  }, [query])

  const move = (delta: number) => {
    if (!filteredItems.length) return
    setActiveIndex(() => {
      const from = Math.min(activeIndex, filteredItems.length - 1)
      return (from + delta + filteredItems.length) % filteredItems.length
    })
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // An IME candidate window uses Enter and the arrows itself; acting on them
    // would run the highlighted entry mid-composition.
    if (e.nativeEvent.isComposing) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      move(1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      move(-1)
    } else if (e.key === 'Enter') {
      if (!activeItem) return
      e.preventDefault()
      activeItem.action()
    }
  }

  return (
    <Modal title="全局快速搜索" onClose={onClose}>
      <div className="quickSearch">
        <div className="quickSearchField">
          <Search className="quickSearchFieldIcon" aria-hidden />
          <input
            ref={inputRef}
            type="text"
            className="quickSearchInput"
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={activeItem ? optionId(activePosition) : undefined}
            aria-autocomplete="list"
            autoComplete="off"
            spellCheck={false}
            placeholder="搜索页面、渠道或模型…"
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
          />
          {query && (
            <button
              type="button"
              className="quickSearchClear"
              aria-label="清空输入"
              onClick={() => {
                setQuery('')
                inputRef.current?.focus()
              }}
            >
              <X aria-hidden />
            </button>
          )}
        </div>

        <ul id={listId} className="quickSearchResults" role="listbox" aria-label="搜索结果">
          {filteredItems.map((item, index) => {
            const Icon = item.icon
            const isActive = index === activePosition
            return (
              <li
                key={item.id}
                id={optionId(index)}
                role="option"
                aria-selected={isActive}
                className={`quickSearchItem ${isActive ? 'selected' : ''}`}
                onMouseEnter={() => setActiveIndex(index)}
                onClick={item.action}
              >
                <span className="quickSearchItemIcon" aria-hidden>
                  <Icon />
                </span>
                <span className="quickSearchItemText">
                  <span className="quickSearchItemHead">
                    <span className="quickSearchItemTitle">{item.title}</span>
                    <span className="quickSearchItemCategory">{item.category}</span>
                  </span>
                  {item.subtitle && <span className="quickSearchItemSub">{item.subtitle}</span>}
                </span>
              </li>
            )
          })}
        </ul>

        {!filteredItems.length && (
          <div className="quickSearchEmpty" role="status">
            {query ? `未找到与「${query}」相关的页面、模型或渠道` : '暂无可搜索的内容'}
          </div>
        )}

        <footer className="quickSearchFoot">
          <span><kbd>↑</kbd> <kbd>↓</kbd> 切换选项</span>
          <span><kbd>Enter</kbd> 打开</span>
          <span><kbd>Esc</kbd> 关闭</span>
        </footer>
      </div>
    </Modal>
  )
}
