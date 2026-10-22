import {readFileSync, readdirSync} from 'node:fs'
import {join} from 'node:path'
import {describe, expect, it} from 'vitest'

/**
 * The styling contract: every class name a component can render must resolve to
 * a rule in the one stylesheet, and every rule in the block this remediation
 * appended must belong to a class a component actually renders. `tsc`, vitest
 * and vite build cannot see either gap, and the redesign showed what happens
 * without them: roughly two hundred class names resolved to no rule, while the
 * sheet also carried rules for surfaces that no longer existed.
 *
 * There is no allowlist for missing rules. The variant vocabulary below is not
 * one: each entry is a value a component assembles through an interpolation (a
 * tone, a size, a state) and styles through its base class. The reverse check
 * near the end of this file enforces the other direction for the appended
 * block, and the variant tests enforce that every declared value has a rule.
 */

// Vitest's jsdom environment resolves module URLs to http, so the project root
// is taken from vitest's own cwd (the web/admin package).
const srcDir = join(process.cwd(), 'src')
const stylesheetPath = join(srcDir, 'styles.css')
const appendedBanner = 'Campus AI Gateway - Modern SaaS Console Redesign'

/**
 * Values a component emits as the dynamic half of a class. They are not
 * selectors of their own: each is styled through its base class
 * (`.statCard.success`, `.btn.sm`, `.circuitChip.half-open`). The forward check
 * must not demand a bare `.success { }`, and the reverse check reads the base
 * class of a selector rather than every token.
 */
const VARIANT_VOCABULARY = new Set([
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
 * Class values built into a variable and put on the element later, which the
 * scanner cannot follow: `const rowClass = [...]` in UsageQuotaSection, the
 * circuit tone returned by `circuitTone`, and the route-mode key `gm.mode`.
 *
 * The scanner's limit, stated rather than papered over: it reads literal text
 * in a className attribute. A class assembled by concatenation across modules,
 * or computed by a function it does not model, is invisible in both directions.
 * This list names the known cases in this tree; the test below asserts each
 * one still occurs in a component, so a rename cannot leave a stale entry.
 */
const VARIABLE_HELD_CLASSES = new Set([
  'advanced',
  'closed',
  'clickRow',
  'drilledRow',
  'half-open',
  // limitClass() returns one of these three for a tri-state quota value.
  'limitInherit',
  'limitSet',
  'limitUnlimited',
  'none',
  'weighted',
])

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
  const completed = [...VARIANT_VOCABULARY].filter((variant) => variant.startsWith(token))
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
    .filter((name) => !styled.has(name) && !VARIANT_VOCABULARY.has(name))
    .sort()
}

function tsxSources(): string[] {
  return filesWithExtension(srcDir, '.tsx').map((file) => readFileSync(file, 'utf8'))
}

/**
 * The base class of every selector in the remediation's appended block.
 *
 * The block is new in this branch, so every rule in it should be backed by a
 * component. Pre-existing selectors above the banner are deliberately not
 * checked: untouched pages may still use them. A selector's base class is its
 * first class token, which is the one a component writes literally; later
 * tokens are variants (`success`, `sm`) emitted through an interpolation.
 */
function appendedBlockBaseClasses(css: string): Set<string> {
  const start = css.indexOf(appendedBanner)
  if (start < 0) throw new Error('redesign banner comment not found in styles.css')
  const block = css.slice(start).replace(/\/\*[\s\S]*?\*\//g, '')
  const bases = new Set<string>()
  for (const match of block.matchAll(/([^{}]+)\{/g)) {
    const prelude = match[1].trim()
    if (!prelude || prelude.startsWith('@')) continue
    for (const selector of prelude.split(',')) {
      const first = selector.match(/\.(-?[A-Za-z_][\w-]*)/)
      if (first) bases.add(first[1])
    }
  }
  return bases
}

function declaredUnion(source: string, declaration: RegExp): string[] {
  const match = source.match(declaration)
  expect(match, `declaration ${declaration} not found`).toBeTruthy()
  return [...(match?.[1] ?? '').matchAll(/'([a-z-]+)'/g)].map((value) => value[1])
}

/**
 * Each declared value must have a selector of its own, except the one the base
 * class already represents (Badge's `neutral` is `.badge`, Button's `md` is
 * `.btn`). This catches the original defect class: a value in the union that
 * renders indistinguishably because no rule mentions it.
 */
function expectVariantRules(css: string, baseClass: string, values: string[], defaultVariant?: string) {
  expect(values.length).toBeGreaterThan(0)
  for (const value of values) {
    const selector =
      value === defaultVariant
        ? new RegExp(`\\.${baseClass}(?![\\w-])`)
        : new RegExp(`\\.${baseClass}\\.${value}(?![\\w-])`)
    expect(
      css,
      `${baseClass} declares "${value}" but styles.css has no matching rule`,
    ).toMatch(selector)
  }
}

describe('style contract', () => {
  it('gives every class name a component can render a rule in styles.css', () => {
    const missing = unmatchedClassNames()
    expect(
      missing,
      `Class names rendered by src/**/*.tsx with no rule in styles.css:\n${missing.join('\n')}`,
    ).toEqual([])
  })

  it('keeps the stylesheet syntactically complete: every block is closed', () => {
    // `vite build` only warns about an unbalanced brace. A missing `}` nests
    // every rule after it inside the previous selector, so those surfaces keep
    // browser defaults while the build stays green — the failure mode this
    // contract exists to catch, one level below class names.
    const css = readFileSync(stylesheetPath, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    let depth = 0
    let lowest = 0
    for (const char of css) {
      if (char === '{') depth++
      if (char === '}') depth--
      lowest = Math.min(lowest, depth)
    }
    expect(lowest, 'styles.css closes a block that was never opened').toBeGreaterThanOrEqual(0)
    expect(depth, 'styles.css has an unclosed block').toBe(0)
  })

  it('styles every variant its primitives declare', () => {
    // The forward contract cannot catch this: a tone is a variant token, so
    // `.statCard.danger` is only checked here. The unions are the source of
    // truth, and `danger` was once in StatCard's with no rule, leaving an error
    // stat visually identical to an ordinary one.
    const css = readFileSync(stylesheetPath, 'utf8')
    const read = (file: string) => readFileSync(join(srcDir, 'components', 'ui', file), 'utf8')

    expectVariantRules(css, 'statCard', declaredUnion(read('StatCard.tsx'), /tone\?:\s*([^\n]+)/))
    expectVariantRules(css, 'btn', declaredUnion(read('Button.tsx'), /ButtonVariant\s*=\s*([^\n]+)/))
    expectVariantRules(css, 'btn', declaredUnion(read('Button.tsx'), /ButtonSize\s*=\s*([^\n]+)/), 'md')
    // `neutral` is the base `.badge` rule; the other tones are modifiers.
    expectVariantRules(css, 'badge', declaredUnion(read('Badge.tsx'), /export type Tone\s*=\s*([^\n]+)/), 'neutral')
    expectVariantRules(css, 'toast', declaredUnion(read('Toast.tsx'), /ToastTone\s*=\s*([^\n]+)/))
  })

  it('references every selector the remediation block defines', () => {
    // The opposite gap: rules left behind by a surface that was renamed or
    // deleted. Scoped to the block this branch appended; a pre-existing
    // selector may belong to an untouched page and is left alone.
    const css = readFileSync(stylesheetPath, 'utf8')
    const bases = appendedBlockBaseClasses(css)
    // Guard against a parsing regression that would make the assertion vacuous.
    expect(bases.size).toBeGreaterThan(50)
    const referenced = new Set<string>()
    for (const file of filesWithExtension(srcDir, '.tsx')) {
      for (const name of referencedClassNames(file)) referenced.add(name)
    }
    const orphaned = [...bases]
      .filter((name) => !referenced.has(name) && !VARIABLE_HELD_CLASSES.has(name))
      .sort()
    expect(
      orphaned,
      `Selectors in the remediation block with no component reference:\n${orphaned.join('\n')}`,
    ).toEqual([])
  })

  it('covers the class names built in variables', () => {
    // The scanner reads className attributes, so a name assembled into
    // `rowClass` (or returned by `circuitTone`) is invisible to both checks.
    // These are the known cases; assert each still exists in a component so a
    // rename cannot leave the exception list silently stale.
    const sources = tsxSources().join('\n')
    const styled = styledClassNames(readFileSync(stylesheetPath, 'utf8'))
    for (const name of VARIABLE_HELD_CLASSES) {
      expect(sources, `${name} is in VARIABLE_HELD_CLASSES but no component builds it`).toContain(`'${name}'`)
      // The forward check cannot see these names, so check their rules here.
      expect(styled.has(name), `${name} is built in a variable but styles.css has no rule for it`).toBe(true)
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
