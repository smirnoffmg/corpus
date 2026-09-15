import { describe, expect, it } from 'vitest'
import type { IndexedSource } from './api'
import { combine, groupManuals, manualName, originalUrl, progress, stateLabel } from './library'

const indexed = (chunks: number, embedded: number, quarantined: number) => ({ stage: 'indexed' as const, chunks, embedded, quarantined })
const scan = (pages: number, recognised: number, failed = false) => ({ stage: 'scan' as const, pages, recognised, failed })

const source = (over: Partial<IndexedSource>): IndexedSource => ({
  stage: 'indexed', kind: 'docs', path: 'nltk/index.html', title: 'nltk · NLTK', indexed_at: '2026-09-14T10:00:00Z',
  chunks: 10, embedded: 10, quarantined: 0, description: '', ocr: false, ...over,
})

describe('progress', () => {
  it('is ready when every chunk has a vector', () => {
    expect(progress(indexed(4, 4, 0))).toEqual({ state: 'ready', fraction: 1 })
  })
  it('is embedding while vectors are missing', () => {
    expect(progress(indexed(4, 1, 0))).toEqual({ state: 'embedding', fraction: 0.25 })
  })
  it('has errors once only quarantined chunks are left', () => {
    expect(progress(indexed(4, 3, 1))).toEqual({ state: 'errors', fraction: 0.75 })
  })
  it('is recognising a scan, by the share of its pages read', () => {
    expect(progress(scan(200, 50))).toEqual({ state: 'recognising', fraction: 0.25 })
  })
  it('gives up on a scan recognition kept failing on', () => {
    expect(progress(scan(200, 50, true))).toEqual({ state: 'errors', fraction: 0.25 })
  })
  it('is waiting when nothing is stored yet', () => {
    expect(progress(indexed(0, 0, 0))).toEqual({ state: 'waiting', fraction: 0 })
  })
})

describe('combine', () => {
  it('sums the pages of an uploaded manual', () => {
    expect(combine([source({ chunks: 3, embedded: 1 }), source({ chunks: 2, embedded: 2, quarantined: 0 })])).toEqual(indexed(5, 3, 0))
  })
  it('follows an uploaded scan through recognition', () => {
    expect(combine([{ stage: 'scan', kind: 'book', path: 'b.pdf', title: 'b', pages: 10, recognised: 4, failed: false }])).toEqual(scan(10, 4))
  })
  it('is waiting while the upload has no row yet', () => {
    expect(combine([])).toEqual(indexed(0, 0, 0))
  })
})

describe('groupManuals', () => {
  it('opens a manual at its index page, or at its shallowest page without one', () => {
    const [m] = groupManuals([
      source({ path: 'numpy/reference/generated/numpy.array.html' }),
      source({ path: 'numpy/user/basics.html' }),
    ])
    expect(m.home).toBe('numpy/user/basics.html')
  })

  it('sums the pages of each manual under its directory name', () => {
    const groups = groupManuals([
      source({ path: 'nltk/index.html', chunks: 3, embedded: 3 }),
      source({ path: 'nltk/howto/tokenize.html', chunks: 5, embedded: 1, quarantined: 1 }),
      source({ path: 'scikit-learn/modules/svm.html', chunks: 2, embedded: 0, indexed_at: '2026-09-14T11:00:00Z' }),
    ])
    expect(groups).toEqual([
      { stage: 'indexed', name: 'nltk', description: '', home: 'nltk/index.html', pages: 2, chunks: 8, embedded: 4, quarantined: 1, indexed_at: '2026-09-14T10:00:00Z' },
      { stage: 'indexed', name: 'scikit-learn', description: '', home: 'scikit-learn/modules/svm.html', pages: 1, chunks: 2, embedded: 0, quarantined: 0, indexed_at: '2026-09-14T11:00:00Z' },
    ])
  })
})

describe('originalUrl', () => {
  it('links a manual section to its page and anchor', () => {
    expect(originalUrl({ kind: 'docs', path: 'scikit-learn/modules/svm.html', anchor: 'multi-class classification' })).toBe(
      'http://localhost:8082/docs/scikit-learn/modules/svm.html#multi-class%20classification',
    )
  })
  it('escapes each path segment but keeps the slashes', () => {
    expect(originalUrl({ kind: 'docs', path: 'my manual/a#b.html' })).toBe('http://localhost:8082/docs/my%20manual/a%23b.html')
  })
  it('opens a book at the PDF page of the passage', () => {
    expect(originalUrl({ kind: 'book', path: 'uploads/Concurrency in Go.pdf', page: 203 })).toBe(
      'http://localhost:8082/books/uploads/Concurrency%20in%20Go.pdf#page=203',
    )
    expect(originalUrl({ kind: 'book', path: 'a.pdf' })).toBe('http://localhost:8082/books/a.pdf')
  })
  it('opens a note in Obsidian at the heading it was cited by', () => {
    expect(originalUrl({ kind: 'vault', path: 'brain/Cross entropy.md', locator: 'What it is > Example' }, 'my vault')).toBe(
      'obsidian://open?vault=my%20vault&file=brain%2FCross%20entropy%23Example',
    )
  })
  it('opens a note without a heading at its top', () => {
    expect(originalUrl({ kind: 'vault', path: 'Daily/2026-09-14.md', locator: '' }, 'obsidian')).toBe(
      'obsidian://open?vault=obsidian&file=Daily%2F2026-09-14',
    )
  })
  it('cannot link a note while the vault name is unknown', () => {
    expect(originalUrl({ kind: 'vault', path: 'note.md' })).toBeNull()
  })
})

describe('manualName', () => {
  it.each([
    ['scikit-learn-docs.zip', 'scikit-learn-docs'],
    ['NLTK Book (2nd ed).zip', 'NLTK-Book-2nd-ed'],
    ['numpy_1.26', 'numpy_1.26'],
    ['Документация.zip', ''],
  ])('%s → %s, as the server would name it', (file, want) => {
    expect(manualName(file)).toBe(want)
  })
})

describe('stateLabel', () => {
  it.each([
    [indexed(0, 0, 0), 'Ждёт индексации'],
    [indexed(10, 4, 0), 'Векторизация 40%'],
    [indexed(10, 10, 0), 'Готово'],
    [indexed(10, 5, 5), '5 фрагментов без вектора'],
    [scan(300, 0), 'Скан: ждёт распознавания'],
    [scan(300, 100), 'Распознаётся 33%'],
    [scan(300, 100, true), 'Скан: распознать не удалось'],
  ])('%o → %s', (counts, want) => {
    expect(stateLabel(counts)).toBe(want)
  })
})
