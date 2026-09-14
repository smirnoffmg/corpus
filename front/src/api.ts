export type Kind = 'book' | 'vault' | 'docs'
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
}

export interface SourceStatus {
  kind: Kind
  path: string
  title: string
  indexed_at: string
  chunks: number
  embedded: number
  quarantined: number
  description: '' | 'draft' | 'checked'
}

export type CSLRecord = Record<string, unknown>

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
  return (await getJSON<{ sources: SourceStatus[] }>(`/sources?${q}`, signal)).sources
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
  kind: 'book' | 'docs'
  path: string
}

// upload uses XMLHttpRequest because fetch still reports no upload progress,
// and a 100 MB manual without a progress bar looks like a hang.
export function upload(
  file: File,
  target: { kind: 'book' } | { kind: 'docs'; manual?: string },
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
