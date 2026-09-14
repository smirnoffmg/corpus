import { describe, expect, it, vi } from 'vitest'
import { ApiError, read, search, searchQuery, sources, upload } from './api'

function respond(status: number, body: unknown) {
  const text = typeof body === 'string' ? body : JSON.stringify(body)
  return vi.fn(async () => new Response(text, { status }))
}

describe('searchQuery', () => {
  it('sends only what is set, with the API parameter names', () => {
    expect(searchQuery({ q: 'агрегат DDD', kind: 'book', mode: 'fts', perSource: 1, titleBoost: 0 })).toBe(
      'q=%D0%B0%D0%B3%D1%80%D0%B5%D0%B3%D0%B0%D1%82+DDD&kind=book&mode=fts&per_source=1&title_boost=0',
    )
    expect(searchQuery({ q: 'x', kind: '', mode: 'hybrid' })).toBe('q=x')
  })
})

describe('requests', () => {
  it('reads hits through the /api prefix', async () => {
    const fetchMock = respond(200, { hits: [{ id: 1 }] })
    vi.stubGlobal('fetch', fetchMock)
    await expect(search({ q: 'svm' })).resolves.toEqual([{ id: 1 }])
    expect(fetchMock).toHaveBeenCalledWith('/api/search?q=svm', expect.anything())
  })

  it('turns an error status into an ApiError carrying the server message', async () => {
    vi.stubGlobal('fetch', respond(404, 'read chunk 9: no rows in result set\n'))
    const err = await read(9).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 404, message: 'read chunk 9: no rows in result set' })
  })

  it('filters sources by kind and prefix', async () => {
    const fetchMock = respond(200, { sources: [] })
    vi.stubGlobal('fetch', fetchMock)
    await sources({ kind: 'docs', prefix: 'nltk/' })
    expect(fetchMock).toHaveBeenCalledWith('/api/sources?kind=docs&prefix=nltk%2F', expect.anything())
  })
})

class FakeXHR {
  static last: FakeXHR
  upload = { onprogress: null as ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null }
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  status = 0
  responseText = ''
  method = ''
  url = ''
  body: FormData | null = null
  constructor() { FakeXHR.last = this }
  open(method: string, url: string) { this.method = method; this.url = url }
  send(body: FormData) { this.body = body }
  finish(status: number, text: string) { this.status = status; this.responseText = text; this.onload?.() }
}

describe('upload', () => {
  it('posts the file, reports progress and resolves with the saved path', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const seen: number[] = []
    const file = new File(['PK'], 'nltk site.zip')

    const done = upload(file, { kind: 'docs', manual: 'nltk' }, (f) => seen.push(f))
    const xhr = FakeXHR.last
    expect(xhr.method).toBe('POST')
    expect(xhr.url).toBe('/api/upload?kind=docs&manual=nltk')
    expect(xhr.body?.get('file')).toBe(file)

    xhr.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 200 })
    xhr.finish(201, '{"kind":"docs","path":"nltk"}')

    await expect(done).resolves.toEqual({ kind: 'docs', path: 'nltk' })
    expect(seen).toEqual([0.25])
  })

  it('rejects with the status and message the server gave', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const done = upload(new File(['%PDF-'], 'a.pdf'), { kind: 'book' }, () => {})
    FakeXHR.last.finish(409, 'already in the library: uploads/a.pdf\n')
    await expect(done).rejects.toMatchObject({ status: 409, message: 'already in the library: uploads/a.pdf' })
  })

  it('rejects with status 0 when the connection fails', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const done = upload(new File(['%PDF-'], 'a.pdf'), { kind: 'book' }, () => {})
    FakeXHR.last.onerror?.()
    await expect(done).rejects.toMatchObject({ status: 0 })
  })
})
