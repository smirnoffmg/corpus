export type Kind = 'book' | 'paper' | 'vault' | 'docs'
export type Mode = 'hybrid' | 'fts' | 'vector'

export interface Hit {
  id: number
  kind: Kind
  title: string
  path: string
  locator: string
  anchor?: string
  page?: number
  rank: number
  snippet: string
  ocr?: boolean
}

export interface Passage {
  kind: Kind
  title: string
  path: string
  locator: string
  anchor?: string
  page?: number
  body: string
  previous?: string
  next?: string
  ocr?: boolean
}

// SourceRow is a row of /sources as the server sends it: an indexed source and
// a scan waiting for recognition share one flat shape there.
export interface SourceRow {
  kind: Kind
  path: string
  title: string
  indexed_at: string
  chunks: number
  embedded: number
  quarantined: number
  description: Description
  ocr?: boolean
  scan_pages?: number
  scan_recognised?: number
  scan_failed?: boolean
  scan_error?: string
}

export type Description = '' | 'draft' | 'checked'

// Indexed counts a source's chunks by how far their vectors have come.
export interface Indexed {
  stage: 'indexed'
  chunks: number
  embedded: number
  quarantined: number
}

// Scan is a book with no text layer, still being recognised; it has no chunks
// and no description until it is indexed.
export interface Scan {
  stage: 'scan'
  pages: number
  recognised: number
  failed: boolean // set aside after repeated failures
  error?: string
}

interface Named {
  kind: Kind
  path: string
  title: string
}

export interface IndexedSource extends Named, Indexed {
  indexed_at: string
  description: Description
  ocr: boolean // text recognised from a scan
}

export type ScanSource = Named & Scan

export type SourceStatus = IndexedSource | ScanSource

// sourceOf turns the flat row into a source that is either one or the other,
// so no screen has to work out from zeros which kind of row it holds.
export function sourceOf(r: SourceRow): SourceStatus {
  const named = { kind: r.kind, path: r.path, title: r.title }
  if (r.chunks === 0 && (r.scan_pages ?? 0) > 0) {
    const scan: ScanSource = { stage: 'scan', ...named, pages: r.scan_pages!, recognised: r.scan_recognised ?? 0, failed: r.scan_failed ?? false }
    return r.scan_error ? { ...scan, error: r.scan_error } : scan
  }
  return {
    stage: 'indexed', ...named, indexed_at: r.indexed_at,
    chunks: r.chunks, embedded: r.embedded, quarantined: r.quarantined,
    description: r.description, ocr: r.ocr ?? false,
  }
}

export type CSLRecord = Record<string, unknown>

// Citation is one entry of a publication's reference list: the line as printed,
// what could be read out of it, and where the library keeps that work when it
// has it.
export interface Citation {
  // references, or the primary studies a systematic review reviewed
  list?: 'references' | 'primary'
  ord: number
  raw: string
  label?: string
  doi?: string
  arxiv?: string
  isbn?: string
  url?: string
  authors?: string
  title?: string
  container?: string
  year?: number
  resolved?: string
  matched_by?: string
  resolved_kind?: Kind
  resolved_path?: string
  resolved_title?: string
}

export interface CitingPaper {
  path: string
  title: string
  citation: Citation
}

export interface Reference {
  key: string
  citekey: string
  csl: CSLRecord
  status: 'draft' | 'checked'
  updated_at: string
}

export interface StyleInfo {
  id: string
  title: string
}

export interface Status {
  sources: number
  chunks: number
  db: string
  embedder: string
  degraded?: string
  vault?: string
}

// SearchParams mirrors every knob /search takes, including the ones the page
// does not show yet: ranking is tuned through them, and a tuning screen will
// need them without a second request type.
export interface SearchParams {
  q: string
  kind?: Kind | ''
  mode?: Mode
  limit?: number
  perSource?: number
  norm?: number
  titleBoost?: number
}

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export function searchQuery(p: SearchParams): string {
  const q = new URLSearchParams({ q: p.q })
  if (p.kind) q.set('kind', p.kind)
  if (p.mode && p.mode !== 'hybrid') q.set('mode', p.mode)
  if (p.limit !== undefined) q.set('limit', String(p.limit))
  if (p.perSource !== undefined) q.set('per_source', String(p.perSource))
  if (p.norm !== undefined) q.set('norm', String(p.norm))
  if (p.titleBoost !== undefined) q.set('title_boost', String(p.titleBoost))
  return q.toString()
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`/api${path}`, { signal })
  if (!res.ok) {
    throw new ApiError(res.status, (await res.text()).trim() || res.statusText)
  }
  return (await res.json()) as T
}

export async function search(p: SearchParams, signal?: AbortSignal): Promise<Hit[]> {
  return (await getJSON<{ hits: Hit[] }>(`/search?${searchQuery(p)}`, signal)).hits
}

export function read(id: number, signal?: AbortSignal): Promise<Passage> {
  return getJSON<Passage>(`/read?id=${id}&neighbours`, signal)
}

export async function sources(filter: { kind?: Kind; prefix?: string }, signal?: AbortSignal): Promise<SourceStatus[]> {
  const q = new URLSearchParams()
  if (filter.kind) q.set('kind', filter.kind)
  if (filter.prefix) q.set('prefix', filter.prefix)
  return (await getJSON<{ sources: SourceRow[] }>(`/sources?${q}`, signal)).sources.map(sourceOf)
}

export async function citations(path: string, signal?: AbortSignal): Promise<Citation[]> {
  const q = new URLSearchParams({ path })
  return (await getJSON<{ citations: Citation[] }>(`/citations?${q}`, signal)).citations
}

export async function citing(path: string, signal?: AbortSignal): Promise<CitingPaper[]> {
  const q = new URLSearchParams({ path })
  return (await getJSON<{ citing: CitingPaper[] }>(`/citations/citing?${q}`, signal)).citing
}

export interface Enriched {
  asked: number
  filled: number
  citations: Citation[]
}

export async function enrichCitations(path: string): Promise<Enriched> {
  const res = await fetch(`/api/citations/enrich?${new URLSearchParams({ path })}`, { method: 'POST' })
  if (!res.ok) throw new ApiError(res.status, (await res.text()).trim() || res.statusText)
  return (await res.json()) as Enriched
}

async function sendJSON<T>(method: string, path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method,
    headers: { 'Content-Type': typeof body === 'string' ? 'application/xml' : 'application/json' },
    body: typeof body === 'string' ? body : JSON.stringify(body),
  })
  if (!res.ok) throw new ApiError(res.status, (await res.text()).trim() || res.statusText)
  return (await res.json()) as T
}

function sourceQuery(kind: Kind, path: string): string {
  return new URLSearchParams({ kind, path }).toString()
}

export function getReference(kind: Kind, path: string, signal?: AbortSignal): Promise<{ key: string; reference: Reference | null }> {
  return getJSON(`/bibliography?${sourceQuery(kind, path)}`, signal)
}

export function saveReference(kind: Kind, path: string, csl: CSLRecord, status: Reference['status']): Promise<Reference> {
  return sendJSON('PUT', `/bibliography?${sourceQuery(kind, path)}`, { csl, status })
}

export async function lookupReference(id: { doi: string } | { isbn: string }): Promise<CSLRecord> {
  return (await sendJSON<{ csl: CSLRecord }>('POST', '/bibliography/lookup', id)).csl
}

export async function styles(signal?: AbortSignal): Promise<StyleInfo[]> {
  return (await getJSON<{ styles?: StyleInfo[] }>('/styles', signal)).styles ?? []
}

export async function styleXML(id: string): Promise<string> {
  const res = await fetch(`/api/styles/${encodeURIComponent(id)}`)
  if (!res.ok) throw new ApiError(res.status, (await res.text()).trim())
  return res.text()
}

export function addStyle(xml: string): Promise<StyleInfo> {
  return sendJSON('POST', '/styles', xml)
}

export function fetchStyle(name: string): Promise<StyleInfo> {
  return sendJSON('POST', '/styles/fetch', { name })
}

export function status(signal?: AbortSignal): Promise<Status> {
  return getJSON<Status>('/status', signal)
}

export interface Uploaded {
  kind: 'book' | 'paper' | 'docs'
  path: string
}

// upload uses XMLHttpRequest because fetch still reports no upload progress,
// and a 100 MB manual without a progress bar looks like a hang.
export function upload(
  file: File,
  target: { kind: 'book' | 'paper' } | { kind: 'docs'; manual?: string },
  onProgress: (fraction: number) => void,
): Promise<Uploaded> {
  const q = new URLSearchParams({ kind: target.kind })
  if (target.kind === 'docs' && target.manual) q.set('manual', target.manual)
  const body = new FormData()
  body.append('file', file)

  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api/upload?${q}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded / e.total)
    }
    xhr.onload = () => {
      if (xhr.status === 201) resolve(JSON.parse(xhr.responseText) as Uploaded)
      else reject(new ApiError(xhr.status, xhr.responseText.trim()))
    }
    xhr.onerror = () => reject(new ApiError(0, 'no connection to the server'))
    xhr.send(body)
  })
}
