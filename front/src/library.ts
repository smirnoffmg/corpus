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
  pages: number
  indexed_at: string
}

// A manual is hundreds of sources, one per page; the library shows it as one
// row, named by its directory, which is also how its citations name it.
export function groupManuals(pages: SourceStatus[]): Manual[] {
  const byName = new Map<string, Manual>()
  for (const p of pages) {
    const name = p.path.split('/')[0]
    const m = byName.get(name) ?? { name, pages: 0, chunks: 0, embedded: 0, quarantined: 0, indexed_at: p.indexed_at }
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
  anchor?: string
  page?: number
}

// originalUrl is where nginx serves the source itself: a manual's own page,
// with its styles and images, scrolled to the section; a book's PDF, opened by
// the browser's viewer at the page (#page= is the PDF open parameter Chrome
// and Firefox honour). A note has none — it lives in Obsidian.
export function originalUrl({ kind, path, anchor, page }: Place): string | null {
  const escaped = path.split('/').map(encodeURIComponent).join('/')
  switch (kind) {
    case 'docs':
      return anchor ? `/docs/${escaped}#${encodeURIComponent(anchor)}` : `/docs/${escaped}`
    case 'book':
      return page ? `/books/${escaped}#page=${page}` : `/books/${escaped}`
    default:
      return null
  }
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
