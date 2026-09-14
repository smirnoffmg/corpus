import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { SourceStatus } from '../api'
import { stubApi } from '../test/fetch'
import { LibraryPage } from './LibraryPage'

const book = (over: Partial<SourceStatus> = {}): SourceStatus => ({
  kind: 'book', path: 'Concurrency in Go.pdf', title: 'Concurrency in Go', indexed_at: '2026-09-14T10:00:00Z',
  chunks: 240, embedded: 240, quarantined: 0, ...over,
})

class FakeXHR {
  static last: FakeXHR
  static answer: { status: number; text: string } = { status: 201, text: '' }
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null }
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  status = 0
  responseText = ''
  url = ''
  constructor() { FakeXHR.last = this }
  open(_method: string, url: string) { this.url = url }
  send() {
    queueMicrotask(() => {
      this.status = FakeXHR.answer.status
      this.responseText = FakeXHR.answer.text
      this.onload?.()
    })
  }
}

function renderLibrary(url = '/library', pollMs = 20) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <LibraryPage pollMs={pollMs} />
    </MemoryRouter>,
  )
}

describe('LibraryPage', () => {
  it('lists books with how far each has come', async () => {
    stubApi((url) => (url.pathname === '/api/sources'
      ? { body: { sources: [book(), book({ path: 'uploads/b.pdf', title: 'B', chunks: 10, embedded: 4 })] } }
      : undefined))
    renderLibrary()

    const rows = await screen.findAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(within(rows[1]).getByText('Готово')).toBeInTheDocument()
    expect(within(rows[2]).getByText('Векторизация 40%')).toBeInTheDocument()
  })

  it('shows a manual as one row with its pages summed', async () => {
    const calls = stubApi(() => ({ body: { sources: [
      { ...book(), kind: 'docs', path: 'nltk/index.html', title: 'nltk · NLTK', chunks: 3, embedded: 3 },
      { ...book(), kind: 'docs', path: 'nltk/howto/tokenize.html', title: 'nltk · Tokenize', chunks: 5, embedded: 4, quarantined: 1 },
    ] } }))
    renderLibrary('/library?kind=docs')

    const rows = await screen.findAllByRole('row')
    expect(calls[0].searchParams.get('kind')).toBe('docs')
    expect(rows).toHaveLength(2)
    expect(within(rows[1]).getByText('nltk')).toBeInTheDocument()
    expect(within(rows[1]).getByText('2 страницы')).toBeInTheDocument()
    expect(within(rows[1]).getByText('1 фрагмент без вектора')).toBeInTheDocument()
  })

  it('narrows the list by title', async () => {
    stubApi(() => ({ body: { sources: [book(), book({ path: 'b.pdf', title: 'Designing Data-Intensive Applications' })] } }))
    renderLibrary()
    const user = userEvent.setup()

    await screen.findAllByRole('row')
    await user.type(screen.getByRole('textbox', { name: 'Фильтр по названию' }), 'data-int')
    expect(screen.getAllByRole('row')).toHaveLength(2)
    expect(screen.getByText('Designing Data-Intensive Applications')).toBeInTheDocument()
  })

  it('uploads a manual and follows it until it is ready', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.answer = { status: 201, text: '{"kind":"docs","path":"nltk"}' }
    let polls = 0
    stubApi((url) => {
      if (url.searchParams.get('prefix') !== 'nltk/') return { body: { sources: [] } }
      polls++
      const embedded = polls === 1 ? 0 : 8
      return { body: { sources: [{ ...book(), kind: 'docs', path: 'nltk/index.html', chunks: 8, embedded }] } }
    })
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файл'), new File(['PK'], 'NLTK site.zip', { type: 'application/zip' }))
    const name = screen.getByRole('textbox', { name: 'Название мануала' })
    expect(name).toHaveValue('NLTK-site')
    await user.clear(name)
    await user.type(name, 'nltk')
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))

    expect(FakeXHR.last.url).toBe('/api/upload?kind=docs&manual=nltk')
    const recent = await screen.findByRole('region', { name: 'Загрузки' })
    expect(await within(recent).findByText('Готово', {}, { timeout: 2000 })).toBeInTheDocument()
  })

  it('uploads a PDF as a book without asking for a name', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.answer = { status: 201, text: '{"kind":"book","path":"uploads/Клеппман.pdf"}' }
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файл'), new File(['%PDF-'], 'Клеппман.pdf', { type: 'application/pdf' }))
    expect(screen.queryByRole('textbox', { name: 'Название мануала' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))

    expect(FakeXHR.last.url).toBe('/api/upload?kind=book')
    const recent = await screen.findByRole('region', { name: 'Загрузки' })
    expect(await within(recent).findByText('Ждёт индексации')).toBeInTheDocument()
  })

  it('says why an upload was refused', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.answer = { status: 409, text: 'already in the library: uploads/a.pdf' }
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файл'), new File(['%PDF-'], 'a.pdf'))
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Такой файл уже есть в библиотеке')
  })

  it('refuses a file it cannot index before sending it', async () => {
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup({ applyAccept: false })

    await user.upload(screen.getByLabelText('Выбрать файл'), new File(['x'], 'book.epub'))
    expect(screen.getByRole('alert')).toHaveTextContent('.pdf')
    expect(screen.getByRole('button', { name: 'Загрузить' })).toBeDisabled()
  })

  it('takes a file dropped onto the upload area', async () => {
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const zone = screen.getByText(/Перетащите сюда/).closest('div')!
    const file = new File(['%PDF-'], 'dropped.pdf')
    await act(async () => {
      zone.dispatchEvent(Object.assign(new Event('drop', { bubbles: true }), { dataTransfer: { files: [file] } }))
    })
    expect(screen.getByText('dropped.pdf')).toBeInTheDocument()
  })
})
