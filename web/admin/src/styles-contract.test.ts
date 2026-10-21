import {readFileSync, readdirSync} from 'node:fs'
import {join} from 'node:path'
import {describe, expect, it} from 'vitest'

/**
 * The styling contract: every class name a component can render must resolve to
 * a rule in the one stylesheet. `tsc`, vitest and vite build cannot see this
 * gap, and the redesign showed what happens without it: roughly two hundred
 * class names resolved to no rule, so whole surfaces fell back to browser
 * defaults while the suite stayed green.
 *
 * Later tickets in this remediation shrink the allowlist below as they align
 * their own surfaces; ticket 10 empties it. A new unmatched name is a failure,
 * not a review note.
 */

// Vitest's jsdom environment resolves module URLs to http, so the project root
// is taken from vitest's own cwd (the web/admin package).
const srcDir = join(process.cwd(), 'src')
const stylesheetPath = join(srcDir, 'styles.css')

/**
 * Tokens a component emits as a dynamic variant (a tone, a size, a state).
 * They are not selectors of their own: each one is styled through its base
 * class (`.statCard.success`, `.btn.sm`, `.latencyBadge.fast`). The contract
 * still requires the base class, and the primitives this ticket owns require
 * every variant they accept to have a rule; this list only stops the scanner
 * from demanding a bare `.ok { }` of its own.
 */
const VARIANT_TOKENS = new Set([
  'active',
  'column',
  'cols-2',
  'cols-3',
  'cols-4',
  'danger',
  'default',
  'down',
  'extraBadge',
  'fast',
  'ghost',
  'hasBadge',
  'info',
  'interactive',
  'lg',
  'limit',
  'login',
  'm',
  'md',
  'mode',
  'neutral',
  'normal',
  'off',
  'ok',
  'on',
  'open',
  'primary',
  'purple',
  'register',
  'secondary',
  'selected',
  'size',
  'slow',
  'sm',
  'status',
  'success',
  'tab',
  'tone',
  'unread',
  'up',
  'warning',
  'wildcard',
])

/**
 * The names this ticket does not fix. It is deliberately the only place a
 * missing rule is tolerated, and it must stay sorted so two tickets appending
 * to it produce a readable conflict. An entry that is no longer unmatched fails
 * the second test, which is how the list shrinks instead of rotting.
 */
const UNSTYLED_CLASS_ALLOWLIST: readonly string[] = [
  'amber',
  'bannerGreeting',
  'bannerGreetingRow',
  'bannerTitleMain',
  'blue',
  'cardNotice',
  'charCountHint',
  'configTile',
  'configTileHead',
  'configTileIcon',
  'configTileName',
  'configTileSub',
  'configTileValue',
  'configTilesGrid',
  'drawerActionFooter',
  'drawerFormStack',
  'fieldLabelWithCount',
  'green',
  'halfPanel',
  'linkActionBtn',
  'modalHeadText',
  'noticeTextContent',
  'panelActionGroup',
  'routeField',
  'routeModeTag',
  'routeModelDot',
  'routeModelLinesCount',
  'routeModelTableWrap',
  'shareBadge',
  'shareBadges',
  'shareGroupTitle',
  'sharePct',
  'sharePriorityGroup',
  'sharesList',
  'sideBySidePanels',
  'statusLatencyTag',
  'statusTitleRow',
  'textSuccess',
  'updatedTimeHint',
]

function filesWithExtension(dir: string, extension: string): string[] {
  const found: string[] = []
  for (const entry of readdirSync(dir, {withFileTypes: true})) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) found.push(...filesWithExtension(full, extension))
    else if (entry.name.endsWith(extension)) found.push(full)
  }
  return found
}

/** Every class name any selector in the sheet mentions, including inside @media. */
function styledClassNames(css: string): Set<string> {
  const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const names = new Set<string>()
  // A selector prelude is whatever precedes `{`. At-rule preludes are skipped
  // because `@keyframes` names and `@media` queries are not class selectors.
  for (const match of withoutComments.matchAll(/([^{}]+)\{/g)) {
    const prelude = match[1].trim()
    if (!prelude || prelude.startsWith('@')) continue
    for (const selector of prelude.matchAll(/\.(-?[A-Za-z_][\w-]*)/g)) names.add(selector[1])
  }
  return names
}

/** Index of the `}` matching the `{` at `openIndex`, skipping strings. */
function matchingBrace(source: string, openIndex: number): number {
  let depth = 0
  for (let i = openIndex; i < source.length; i++) {
    const char = source[i]
    if (char === "'" || char === '"') {
      i = skipQuoted(source, i)
      continue
    }
    if (char === '`') {
      i = skipTemplate(source, i)
      continue
    }
    if (char === '{') depth++
    if (char === '}') {
      depth--
      if (depth === 0) return i
    }
  }
  throw new Error(`unbalanced className expression at offset ${openIndex}`)
}

/** Index of the quote closing the string opened at `openIndex`. */
function skipQuoted(source: string, openIndex: number): number {
  const quote = source[openIndex]
  for (let i = openIndex + 1; i < source.length; i++) {
    if (source[i] === '\\') {
      i++
      continue
    }
    if (source[i] === quote) return i
  }
  throw new Error(`unterminated string at offset ${openIndex}`)
}

/** Index of the backtick closing the template literal opened at `openIndex`. */
function skipTemplate(source: string, openIndex: number): number {
  for (let i = openIndex + 1; i < source.length; i++) {
    if (source[i] === '\\') {
      i++
      continue
    }
    if (source[i] === '$' && source[i + 1] === '{') {
      i = matchingBrace(source, i + 1)
      continue
    }
    if (source[i] === '`') return i
  }
  throw new Error(`unterminated template literal at offset ${openIndex}`)
}

/**
 * Class names from one className attribute value, bounded by `[from, to)`.
 *
 * Only literal text counts. The identifier in `className={`pageBanner ${className}`}`
 * is a variable name, not a class; the quoted strings inside a conditional
 * (`'ok'`, `'selectedRow'`) are values that do land in the DOM, so they are
 * collected and then resolved as variant tokens or checked against the sheet.
 */
function classNameLiterals(source: string, from: number, to: number): string[] {
  const names: string[] = []
  let i = from
  while (i < to) {
    const char = source[i]
    if (char === "'" || char === '"') {
      const end = skipQuoted(source, i)
      names.push(...literalTokens(source.slice(i + 1, end)))
      i = end + 1
      continue
    }
    if (char === '`') {
      const end = skipTemplate(source, i)
      names.push(...templateTokens(source, i + 1, end))
      i = end + 1
      continue
    }
    i++
  }
  return names
}

function literalTokens(text: string): string[] {
  return text.split(/\s+/).filter(Boolean)
}

/**
 * Tokens from a template literal body: literal segments split on whitespace,
 * plus the quoted values of every `${...}` expression. A segment ending in `-`
 * (`cols-` in `cols-${columns}`) is a prefix completed by an interpolation, so
 * it is expanded to the variant values the component accepts instead of being
 * reported as a name no rule can define.
 */
function templateTokens(source: string, from: number, to: number): string[] {
  const names: string[] = []
  let i = from
  while (i < to) {
    const char = source[i]
    if (char === '$' && source[i + 1] === '{') {
      const end = matchingBrace(source, i + 1)
      names.push(...quotedValues(source, i + 2, end))
      i = end + 1
      continue
    }
    let end = i
    while (end < to && source[end] !== '$') end++
    for (const token of source.slice(i, end).split(/\s+/)) {
      if (!/^[a-zA-Z][\w-]*$/.test(token)) continue
      names.push(...expandInterpolatedPrefix(token))
    }
    i = end
  }
  return names
}

/**
 * Quoted string literals inside an interpolation, e.g. the `'ok'` in
 * `${x ? 'ok' : 'down'}`. A literal on either side of a comparison is an
 * operand, not a class: in `${tab === 'keys' ? 'on' : ''}` only `on` reaches
 * the DOM, and reporting `keys` would force a bogus allowlist entry.
 */
function quotedValues(source: string, from: number, to: number): string[] {
  const values: string[] = []
  let i = from
  while (i < to) {
    const char = source[i]
    if (char === "'" || char === '"') {
      const end = skipQuoted(source, i)
      if (!isComparisonOperand(source, i, end)) {
        values.push(...literalTokens(source.slice(i + 1, end)))
      }
      i = end + 1
      continue
    }
    if (char === '`') {
      i = skipTemplate(source, i) + 1
      continue
    }
    i++
  }
  return values
}

function isComparisonOperand(source: string, start: number, end: number): boolean {
  const before = source.slice(Math.max(0, start - 3), start).trimEnd()
  const after = source.slice(end + 1, end + 4).trimStart()
  const operators = ['===', '!==', '==', '!=']
  return operators.some((op) => before.endsWith(op) || after.startsWith(op))
}

function expandInterpolatedPrefix(token: string): string[] {
  if (!token.endsWith('-')) return [token]
  const completed = [...VARIANT_TOKENS].filter((variant) => variant.startsWith(token))
  return completed.length ? completed : [token]
}

function referencedClassNames(file: string): string[] {
  const source = readFileSync(file, 'utf8')
  const names: string[] = []
  // `className =` also matches the destructured prop declaration; harmless,
  // because the value that follows such a declaration is an empty default and
  // yields no class tokens.
  for (const attribute of source.matchAll(/className\s*=\s*/g)) {
    const valueStart = (attribute.index ?? 0) + attribute[0].length
    const char = source[valueStart]
    if (char === '"' || char === "'") {
      names.push(...literalTokens(source.slice(valueStart + 1, skipQuoted(source, valueStart))))
    } else if (char === '{') {
      const valueEnd = matchingBrace(source, valueStart)
      names.push(...classNameLiterals(source, valueStart + 1, valueEnd))
    }
  }
  return names
}

function unmatchedClassNames(): string[] {
  const styled = styledClassNames(readFileSync(stylesheetPath, 'utf8'))
  const referenced = new Set<string>()
  for (const file of filesWithExtension(srcDir, '.tsx')) {
    for (const name of referencedClassNames(file)) referenced.add(name)
  }
  return [...referenced]
    .filter((name) => !styled.has(name) && !VARIANT_TOKENS.has(name))
    .sort()
}

describe('style contract', () => {
  it('gives every class name a component can render a rule in styles.css', () => {
    const allowed = new Set(UNSTYLED_CLASS_ALLOWLIST)
    const missing = unmatchedClassNames().filter((name) => !allowed.has(name))
    expect(
      missing,
      `Class names rendered by src/**/*.tsx with no rule in styles.css:\n${missing.join('\n')}\n\n` +
        'Add the rule, or add the name to UNSTYLED_CLASS_ALLOWLIST only if a later ticket owns it.',
    ).toEqual([])
  })

  it('keeps UNSTYLED_CLASS_ALLOWLIST sorted and free of stale entries', () => {
    // Sorted: two tickets appending at the same place produce one conflict.
    expect(UNSTYLED_CLASS_ALLOWLIST).toEqual([...UNSTYLED_CLASS_ALLOWLIST].sort())
    // No duplicates.
    expect(new Set(UNSTYLED_CLASS_ALLOWLIST).size).toBe(UNSTYLED_CLASS_ALLOWLIST.length)
    // Every entry still names a real, still-unstyled class. A rule added for a
    // name left in the list would otherwise sit there forever.
    const unmatched = new Set(unmatchedClassNames())
    const stale = UNSTYLED_CLASS_ALLOWLIST.filter((name) => !unmatched.has(name))
    expect(stale, `Allowlisted names that now resolve or are no longer rendered:\n${stale.join('\n')}`).toEqual([])
  })

  it('styles every tone StatCard accepts', () => {
    // The contract above cannot catch this: a tone is a variant token, so
    // `.statCard.danger` is only checked here. The union in StatCard.tsx is the
    // source of truth, and `danger` was once in the union with no rule, leaving
    // an error stat visually identical to an ordinary one.
    const statCardSource = readFileSync(join(srcDir, 'components', 'ui', 'StatCard.tsx'), 'utf8')
    const union = statCardSource.match(/tone\?:\s*([^\n]+)/)
    expect(union, 'tone union not found in StatCard.tsx').toBeTruthy()
    const tones = [...(union?.[1] ?? '').matchAll(/'([a-z]+)'/g)].map((match) => match[1])
    expect(tones.length).toBeGreaterThan(0)
    const css = readFileSync(stylesheetPath, 'utf8')
    for (const tone of tones) {
      expect(css, `StatCard accepts tone="${tone}" but styles.css has no .statCard.${tone} rule`).toMatch(
        new RegExp(`\\.statCard\\.${tone}(?![\\w-])`),
      )
    }
  })

  it('does not let the redesign block redefine a selector that predates it', () => {
    // The appended block once redefined `.filterField` and `.statusPill`, which
    // silently restyled the untouched request-log page and the dashboard status
    // row. New surfaces get namespaced classes instead.
    const css = readFileSync(stylesheetPath, 'utf8')
    const blockStart = css.indexOf('Campus AI Gateway - Modern SaaS Console Redesign')
    expect(blockStart, 'redesign banner comment not found in styles.css').toBeGreaterThan(-1)
    // Comments are stripped before the search: they legitimately mention the
    // primitive they explain, only a rule would change the page. Pseudo-classes
    // and elements target the primitive too, so they count as a redefinition.
    const block = css.slice(blockStart).replace(/\/\*[\s\S]*?\*\//g, '')
    for (const primitive of ['panel', 'filters', 'filterField', 'statusPill']) {
      // The trailing guard keeps `.filtersBar` and `.filterFieldInline` out;
      // only a selector that really targets the primitive is a redefinition.
      expect(block, `redesign block redefines .${primitive}`).not.toMatch(
        new RegExp(`\\.${primitive}(?![\\w-])`),
      )
    }
  })
})
