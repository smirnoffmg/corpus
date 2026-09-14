import { describe, expect, it } from 'vitest'
import type { SourceStatus } from './api'
import { groupManuals, manualName, originalUrl, progress, stateLabel } from './library'

const source = (over: Partial<SourceStatus>): SourceStatus => ({
  kind: 'docs', path: 'nltk/index.html', title: 'nltk · NLTK', indexed_at: '2026-09-14T10:00:00Z',
  chunks: 10, embedded: 10, quarantined: 0, description: '', ...over,
})

describe('progress', () => {
  it('is ready when every chunk has a vector', () => {
    expect(progress({ chunks: 4, embedded: 4, quarantined: 0 })).toEqual({ state: 'ready', fraction: 1 })
  })
  it('is embedding while vectors are missing', () => {
    expect(progress({ chunks: 4, embedded: 1, quarantined: 0 })).toEqual({ state: 'embedding', fraction: 0.25 })
  })
  it('has errors once only quarantined chunks are left', () => {
    expect(progress({ chunks: 4, embedded: 3, quarantined: 1 })).toEqual({ state: 'errors', fraction: 0.75 })
  })
  it('is waiting when nothing is stored yet', () => {
    expect(progress({ chunks: 0, embedded: 0, quarantined: 0 })).toEqual({ state: 'waiting', fraction: 0 })
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
      { name: 'nltk', description: '', home: 'nltk/index.html', pages: 2, chunks: 8, embedded: 4, quarantined: 1, indexed_at: '2026-09-14T10:00:00Z' },
      { name: 'scikit-learn', description: '', home: 'scikit-learn/modules/svm.html', pages: 1, chunks: 2, embedded: 0, quarantined: 0, indexed_at: '2026-09-14T11:00:00Z' },
    ])
  })
})

describe('originalUrl', () => {
  it('links a manual section to its page and anchor', () => {
    expect(originalUrl({ kind: 'docs', path: 'scikit-learn/modules/svm.html', anchor: 'multi-class classification' })).toBe(
      '/docs/scikit-learn/modules/svm.html#multi-class%20classification',
    )
  })
  it('escapes each path segment but keeps the slashes', () => {
    expect(originalUrl({ kind: 'docs', path: 'my manual/a#b.html' })).toBe('/docs/my%20manual/a%23b.html')
  })
  it('opens a book at the PDF page of the passage', () => {
    expect(originalUrl({ kind: 'book', path: 'uploads/Concurrency in Go.pdf', page: 203 })).toBe(
      '/books/uploads/Concurrency%20in%20Go.pdf#page=203',
    )
    expect(originalUrl({ kind: 'book', path: 'a.pdf' })).toBe('/books/a.pdf')
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
    [{ chunks: 0, embedded: 0, quarantined: 0 }, 'Ждёт индексации'],
    [{ chunks: 10, embedded: 4, quarantined: 0 }, 'Векторизация 40%'],
    [{ chunks: 10, embedded: 10, quarantined: 0 }, 'Готово'],
    [{ chunks: 10, embedded: 5, quarantined: 5 }, '5 фрагментов без вектора'],
  ])('%o → %s', (counts, want) => {
    expect(stateLabel(counts)).toBe(want)
  })
})
