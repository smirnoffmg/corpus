import { plural } from './text'
import type { Kind, SourceStatus } from './api'

export type State = 'waiting' | 'embedding' | 'ready' | 'errors'

type Counts = Pick<SourceStatus, 'chunks' | 'embedded' | 'quarantined'>

export function progress({ chunks, embedded, quarantined }: Counts): { state: State; fraction: number } {
  if (chunks === 0) return { state: 'waiting', fraction: 0 }
  const fraction = embedded / chunks
  if (embedded === chunks) return { state: 'ready', fraction }
  if (embedded + quarantined === chunks) return { state: 'errors', fraction }
  return { state: 'embedding', fraction }
}

export interface Manual extends Counts {
  name: string
  description: SourceStatus['description']
  home: string // the page a manual opens at
  pages: number
  indexed_at: string
}

// A manual is hundreds of sources, one per page; the library shows it as one
// row, named by its directory, which is also how its citations name it.
export function groupManuals(pages: SourceStatus[]): Manual[] {
  const byName = new Map<string, Manual>()
  for (const p of pages) {
    const name = p.path.split('/')[0]
    const m = byName.get(name) ?? { name, description: p.description, home: p.path, pages: 0, chunks: 0, embedded: 0, quarantined: 0, indexed_at: p.indexed_at }
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

// originalUrl is where nginx serves the source itself: a manual's own page,
// with its styles and images, scrolled to the section; a book's PDF, opened by
// the browser's viewer at the page (#page= is the PDF open parameter Chrome
// and Firefox honour). A note opens in Obsidian itself, at the last heading of
// its citation — an obsidian:// file may name one heading, not a path of them.
export function originalUrl({ kind, path, locator, anchor, page }: Place, vault?: string): string | null {
  const escaped = path.split('/').map(encodeURIComponent).join('/')
  switch (kind) {
    case 'docs':
      return anchor ? `/docs/${escaped}#${encodeURIComponent(anchor)}` : `/docs/${escaped}`
    case 'book':
      return page ? `/books/${escaped}#page=${page}` : `/books/${escaped}`
    case 'vault': {
      if (!vault) return null
      const heading = locator ? locator.split(' > ').pop() : ''
      const file = path.replace(/\.md$/i, '') + (heading ? `#${heading}` : '')
      return `obsidian://open?vault=${encodeURIComponent(vault)}&file=${encodeURIComponent(file)}`
    }
  }
}

export function originalLabel(kind: Kind): string {
  return kind === 'vault' ? 'Открыть в Obsidian' : 'Открыть оригинал'
}

const withoutVector: [string, string, string] = ['фрагмент без вектора', 'фрагмента без вектора', 'фрагментов без вектора']

export function stateLabel(counts: Counts): string {
  const { state, fraction } = progress(counts)
  switch (state) {
    case 'waiting':
      return 'Ждёт индексации'
    case 'embedding':
      return `Векторизация ${Math.floor(fraction * 100)}%`
    case 'ready':
      return 'Готово'
    case 'errors':
      return `${counts.quarantined} ${plural(counts.quarantined, withoutVector)}`
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
