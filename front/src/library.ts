import { plural } from './text'
import type { Description, Indexed, IndexedSource, Kind, Scan, SourceStatus } from './api'

export type State = 'waiting' | 'recognising' | 'embedding' | 'ready' | 'errors'

// Tally is how far a source, or a group of them, has come.
export type Tally = Indexed | Scan

export function progress(t: Tally): { state: State; fraction: number } {
  if (t.stage === 'scan') return { state: t.failed ? 'errors' : 'recognising', fraction: t.recognised / t.pages }
  const { chunks, embedded, quarantined } = t
  if (chunks === 0) return { state: 'waiting', fraction: 0 }
  const fraction = embedded / chunks
  if (embedded === chunks) return { state: 'ready', fraction }
  if (embedded + quarantined === chunks) return { state: 'errors', fraction }
  return { state: 'embedding', fraction }
}

export const nothingYet: Indexed = { stage: 'indexed', chunks: 0, embedded: 0, quarantined: 0 }

function tallyOf(s: SourceStatus): Tally {
  return s.stage === 'scan'
    ? { stage: 'scan', pages: s.pages, recognised: s.recognised, failed: s.failed }
    : { stage: 'indexed', chunks: s.chunks, embedded: s.embedded, quarantined: s.quarantined }
}

// combine is the progress of one upload: a book is one source, which may be a
// scan; a manual is its pages, each indexed.
export function combine(rows: SourceStatus[]): Tally {
  if (rows.length === 1) return tallyOf(rows[0])
  return rows.reduce<Indexed>(
    (sum, s) => (s.stage === 'indexed'
      ? { stage: 'indexed', chunks: sum.chunks + s.chunks, embedded: sum.embedded + s.embedded, quarantined: sum.quarantined + s.quarantined }
      : sum),
    nothingYet,
  )
}

export interface Manual extends Indexed {
  name: string
  description: Description
  home: string // the page a manual opens at
  pages: number
  indexed_at: string
}

// A manual is hundreds of sources, one per page; the library shows it as one
// row, named by its directory, which is also how its citations name it.
export function groupManuals(pages: IndexedSource[]): Manual[] {
  const byName = new Map<string, Manual>()
  for (const p of pages) {
    const name = p.path.split('/')[0]
    const m = byName.get(name) ?? { stage: 'indexed', name, description: p.description, home: p.path, pages: 0, chunks: 0, embedded: 0, quarantined: 0, indexed_at: p.indexed_at }
    if (homelier(p.path, m.home, name)) m.home = p.path
    m.pages++
    m.chunks += p.chunks
    m.embedded += p.embedded
    m.quarantined += p.quarantined
    if (p.indexed_at > m.indexed_at) m.indexed_at = p.indexed_at
    byName.set(name, m)
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export interface Place {
  kind: Kind
  path: string
  locator?: string
  anchor?: string
  page?: number
}

// homelier says whether a page is a better front door to its manual than the
// current one: the manual's own index.html, failing that the shallowest page —
// nginx will not list a directory, so the manual has to open at a real page.
function homelier(candidate: string, current: string, manual: string): boolean {
  const index = `${manual}/index.html`
  if (current === index) return false
  if (candidate === index) return true
  const depth = (path: string) => path.split('/').length
  return depth(candidate) < depth(current) || (depth(candidate) === depth(current) && candidate < current)
}

// sourcesOrigin is where PDFs and manual pages are served: the same host as
// the UI on a port of its own. A manual's scripts must not run on the UI's
// origin, where they could read the library through /api (see nginx.conf).
export function sourcesOrigin(): string {
  const port = import.meta.env.VITE_SOURCES_PORT ?? '8082'
  return `${window.location.protocol}//${window.location.hostname}:${port}`
}

// originalUrl is where nginx serves the source itself: a manual's own page,
// with its styles and images, scrolled to the section; a book's or a
// publication's PDF, opened by the browser's viewer at the page (#page= is the PDF open parameter Chrome
// and Firefox honour). A note opens in Obsidian itself, at the last heading of
// its citation — an obsidian:// file may name one heading, not a path of them.
export function originalUrl({ kind, path, locator, anchor, page }: Place, vault?: string): string | null {
  const escaped = path.split('/').map(encodeURIComponent).join('/')
  switch (kind) {
    case 'docs':
      return anchor ? `${sourcesOrigin()}/docs/${escaped}#${encodeURIComponent(anchor)}` : `${sourcesOrigin()}/docs/${escaped}`
    case 'book':
      return page ? `${sourcesOrigin()}/books/${escaped}#page=${page}` : `${sourcesOrigin()}/books/${escaped}`
    case 'paper':
      return page ? `${sourcesOrigin()}/papers/${escaped}#page=${page}` : `${sourcesOrigin()}/papers/${escaped}`
    case 'vault': {
      if (!vault) return null
      const heading = locator ? locator.split(' > ').pop() : ''
      const file = path.replace(/\.md$/i, '') + (heading ? `#${heading}` : '')
      return `obsidian://open?vault=${encodeURIComponent(vault)}&file=${encodeURIComponent(file)}`
    }
  }
}

// paperHref is a publication's card: what it is, what it cites, what cites it.
export function paperHref(path: string): string {
  return `/paper?${new URLSearchParams({ path })}`
}

export function originalLabel(kind: Kind): string {
  return kind === 'vault' ? 'Открыть в Obsidian' : 'Открыть оригинал'
}

const withoutVector: [string, string, string] = ['фрагмент без вектора', 'фрагмента без вектора', 'фрагментов без вектора']

export function stateLabel(t: Tally): string {
  const { state, fraction } = progress(t)
  if (t.stage === 'scan') {
    if (state === 'errors') return 'Скан: распознать не удалось'
    return t.recognised ? `Распознаётся ${Math.floor(fraction * 100)}%` : 'Скан: ждёт распознавания'
  }
  switch (state) {
    case 'embedding':
      return `Векторизация ${Math.floor(fraction * 100)}%`
    case 'ready':
      return 'Готово'
    case 'errors':
      return `${t.quarantined} ${plural(t.quarantined, withoutVector)}`
    default:
      return 'Ждёт индексации'
  }
}

// manualName is the server's naming rule (upload.ManualName), repeated so the
// form can show the name before the upload rather than after it.
export function manualName(file: string): string {
  const base = file.split(/[/\\]/).pop()!.trim().replace(/\.zip$/i, '')
  return base
    .replace(/[^A-Za-z0-9_.-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^[-.]+|[-.]+$/g, '')
}
