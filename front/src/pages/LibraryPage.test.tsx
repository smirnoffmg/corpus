import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { SourceRow } from '../api'
import { stubApi } from '../test/fetch'
import { VaultContext } from '../vault'
import { LibraryPage } from './LibraryPage'

const book = (over: Partial<SourceRow> = {}): SourceRow => ({
  kind: 'book', path: 'Concurrency in Go.pdf', title: 'Concurrency in Go', indexed_at: '2026-09-14T10:00:00Z',
  chunks: 240, embedded: 240, quarantined: 0, description: '', ...over,
})

type Answer = { status: number; text: string }

class FakeXHR {
  static last: FakeXHR
  static answer: Answer = { status: 201, text: '' }
  // Per file name, for a batch whose files the server answers differently.
  static answers: Record<string, Answer> = {}
  static sent: string[] = []
  static active = 0
  static maxActive = 0
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null }
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  status = 0
  responseText = ''
  url = ''
  constructor() { FakeXHR.last = this }
  static reset() {
    FakeXHR.answers = {}
    FakeXHR.sent = []
    FakeXHR.active = 0
    FakeXHR.maxActive = 0
  }
  open(_method: string, url: string) { this.url = url }
  send(body: FormData) {
    const name = (body.get('file') as File).name
    FakeXHR.sent.push(`${name} -> ${this.url}`)
    FakeXHR.active++
    FakeXHR.maxActive = Math.max(FakeXHR.maxActive, FakeXHR.active)
    setTimeout(() => {
      const answer = FakeXHR.answers[name] ?? FakeXHR.answer
      this.status = answer.status
      this.responseText = answer.text
      FakeXHR.active--
      this.onload?.()
    }, 5)
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
      ? { body: { sources: [book(), book({ path: 'uploads/b.pdf', title: 'B', chunks: 10, embedded: 4, description: 'checked' })] } }
      : undefined))
    renderLibrary()

    const rows = await screen.findAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(within(rows[1]).getByText('Готово')).toBeInTheDocument()
    expect(within(rows[1]).getByRole('link', { name: 'Concurrency in Go' })).toHaveAttribute('href', 'http://localhost:8082/books/Concurrency%20in%20Go.pdf')
    expect(within(rows[2]).getByText('Векторизация 40%')).toBeInTheDocument()
    expect(within(rows[1]).getByRole('link', { name: 'Описание: Concurrency in Go' })).toHaveTextContent('Описать')
    expect(within(rows[1]).getByRole('link', { name: 'Описание: Concurrency in Go' })).toHaveAttribute('href', '/describe?kind=book&path=Concurrency+in+Go.pdf')
  })

  it('shows a scan being recognised, without a description to fill in yet', async () => {
    stubApi((url) => (url.pathname === '/api/sources'
      ? { body: { sources: [book({ path: 'bringhurst.pdf', title: 'bringhurst', chunks: 0, embedded: 0, scan_pages: 400, scan_recognised: 100 }), book({ ocr: true, path: 'nlp.pdf', title: 'NLP' })] } }
      : undefined))
    renderLibrary()

    const scan = (await screen.findByText('bringhurst')).closest('tr')!
    expect(within(scan).getByText('Распознаётся 25%')).toBeInTheDocument()
    expect(within(scan).queryByRole('link', { name: /Описание/ })).toBeNull()
    const recognised = screen.getByText('NLP').closest('tr')!
    expect(within(recognised).getByText('Распознано')).toBeInTheDocument()
  })

  it('shows a manual as one row with its pages summed', async () => {
    const calls = stubApi(() => ({ body: { sources: [
      { ...book(), kind: 'docs', path: 'nltk/index.html', title: 'nltk · NLTK', chunks: 3, embedded: 3 },
      { ...book(), kind: 'docs', path: 'nltk/howto/tokenize.html', title: 'nltk · Tokenize', chunks: 5, embedded: 4, quarantined: 1 },
    ] } }))
    renderLibrary('/library?kind=docs')

    const rows = await screen.findAllByRole('row')
    expect(calls.find((c) => c.pathname === '/api/sources')?.searchParams.get('kind')).toBe('docs')
    expect(rows).toHaveLength(2)
    expect(within(rows[1]).getByRole('link', { name: 'nltk' })).toHaveAttribute('href', 'http://localhost:8082/docs/nltk/index.html')
    expect(within(rows[1]).getByRole('link', { name: 'Описание: nltk' })).toHaveAttribute('href', '/describe?kind=docs&path=nltk%2Findex.html')
    expect(within(rows[1]).getByText('2 страницы')).toBeInTheDocument()
    expect(within(rows[1]).getByText('1 фрагмент без вектора')).toBeInTheDocument()
  })

  it('links a note to Obsidian once the vault is known', async () => {
    stubApi(() => ({ body: { sources: [{ ...book(), kind: 'vault', path: 'Daily/2026-09-14.md', title: '2026-09-14' }] } }))
    render(
      <VaultContext value="obsidian">
        <MemoryRouter initialEntries={['/library?kind=vault']}>
          <LibraryPage />
        </MemoryRouter>
      </VaultContext>,
    )
    const link = await screen.findByRole('link', { name: '2026-09-14' })
    expect(link).toHaveAttribute('href', 'obsidian://open?vault=obsidian&file=Daily%2F2026-09-14')
    expect(link).not.toHaveAttribute('target')
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

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['PK'], 'NLTK site.zip', { type: 'application/zip' }))
    const name = screen.getByRole('textbox', { name: 'Название мануала для «NLTK site.zip»' })
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

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['%PDF-'], 'Клеппман.pdf', { type: 'application/pdf' }))
    expect(screen.queryByRole('textbox', { name: /Название мануала/ })).toBeNull()
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

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['%PDF-'], 'a.pdf'))
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Такой файл уже есть в библиотеке')
  })

  it('refuses a file it cannot index before sending it', async () => {
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup({ applyAccept: false })

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['x'], 'book.epub'))
    expect(screen.getByText(/book\.epub/).closest('li')).toHaveTextContent('.pdf')
    expect(screen.getByRole('button', { name: 'Загрузить' })).toBeDisabled()
  })

  it('takes several files dropped onto the upload area at once', async () => {
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const zone = screen.getByText(/Перетащите сюда/).closest('div')!
    const files = [new File(['%PDF-'], 'first.pdf'), new File(['%PDF-'], 'second.pdf')]
    await act(async () => {
      zone.dispatchEvent(Object.assign(new Event('drop', { bubbles: true }), { dataTransfer: { files } }))
    })
    const queue = screen.getByRole('region', { name: 'Очередь загрузки' })
    expect(within(queue).getByText('first.pdf')).toBeInTheDocument()
    expect(within(queue).getByText('second.pdf')).toBeInTheDocument()
  })

  // A shelf of books goes up in one go. One at a time, so a slow disk or a big
  // manual is not multiplied, and a refusal stops nothing but its own file.
  it('uploads a batch one file after another, and a refused file stops only itself', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.reset()
    FakeXHR.answers = {
      'a.pdf': { status: 201, text: '{"kind":"book","path":"uploads/a.pdf"}' },
      'b.pdf': { status: 409, text: 'already in the library: uploads/b.pdf' },
      'site.zip': { status: 201, text: '{"kind":"docs","path":"site"}' },
    }
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файлы'), [
      new File(['%PDF-'], 'a.pdf'), new File(['%PDF-'], 'b.pdf'), new File(['PK'], 'site.zip'),
    ])
    expect(screen.getByText('3 файла')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))

    const recent = await screen.findByRole('region', { name: 'Загрузки' })
    expect(await within(recent).findByText('site.zip')).toBeInTheDocument()
    expect(within(recent).getByText('a.pdf')).toBeInTheDocument()
    expect(FakeXHR.sent).toEqual([
      'a.pdf -> /api/upload?kind=book',
      'b.pdf -> /api/upload?kind=book',
      'site.zip -> /api/upload?kind=docs&manual=site',
    ])
    expect(FakeXHR.maxActive).toBe(1)

    const queue = screen.getByRole('region', { name: 'Очередь загрузки' })
    const refused = within(queue).getByText('b.pdf').closest('li')!
    expect(refused).toHaveTextContent('Такой файл уже есть в библиотеке')
    expect(within(queue).queryByText('a.pdf')).toBeNull()
  })

  it('adds files chosen later to the queue, and a file can be taken out before sending', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.reset()
    FakeXHR.answer = { status: 201, text: '{"kind":"book","path":"uploads/kept.pdf"}' }
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['%PDF-'], 'kept.pdf'))
    await user.upload(screen.getByLabelText('Выбрать файлы'), [new File(['%PDF-'], 'dropped.pdf'), new File(['%PDF-'], 'kept.pdf')])
    const queue = screen.getByRole('region', { name: 'Очередь загрузки' })
    expect(within(queue).getAllByRole('listitem')).toHaveLength(2)

    await user.click(within(queue).getByRole('button', { name: 'Убрать «dropped.pdf»' }))
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))
    await screen.findByRole('region', { name: 'Загрузки' })
    expect(FakeXHR.sent).toEqual(['kept.pdf -> /api/upload?kind=book'])
  })

  it('sends the files it can index and leaves the rest listed with the reason', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    FakeXHR.reset()
    FakeXHR.answer = { status: 201, text: '{"kind":"book","path":"uploads/good.pdf"}' }
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup({ applyAccept: false })

    await user.upload(screen.getByLabelText('Выбрать файлы'), [new File(['x'], 'novel.epub'), new File(['%PDF-'], 'good.pdf')])
    expect(screen.getByRole('button', { name: 'Загрузить' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Загрузить' }))

    await screen.findByRole('region', { name: 'Загрузки' })
    expect(FakeXHR.sent).toEqual(['good.pdf -> /api/upload?kind=book'])
    expect(screen.getByText('novel.epub').closest('li')).toHaveTextContent('не подойдёт')
  })

  it('will not send a manual without a name', async () => {
    stubApi(() => ({ body: { sources: [] } }))
    renderLibrary()
    const user = userEvent.setup()

    await user.upload(screen.getByLabelText('Выбрать файлы'), new File(['PK'], 'Документация.zip'))
    expect(screen.getByRole('textbox', { name: 'Название мануала для «Документация.zip»' })).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Загрузить' })).toBeDisabled()
  })
})
