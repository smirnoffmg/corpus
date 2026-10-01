import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { SourceRow, Status } from '../api'
import { stubApi } from '../test/fetch'
import { SystemPage } from './SystemPage'

const status = (over: Partial<Status> = {}): Status => ({
  sources: 3, chunks: 16, db: 'ok', embedder: 'ok', reranker: 'unreachable', uploads: false,
  searches_without_vectors: 2, hnsw_ef_search: 200,
  settings: { model: 'bge-m3', ollama: 'http://host.docker.internal:11434' },
  ...over,
})

const book = (over: Partial<SourceRow> = {}): SourceRow => ({
  kind: 'book', path: 'a.pdf', title: 'A', indexed_at: '2026-09-14T10:00:00Z',
  chunks: 10, embedded: 5, quarantined: 0, description: '', ...over,
})

function renderSystem(pollMs = 20) {
  return render(
    <MemoryRouter>
      <SystemPage pollMs={pollMs} />
    </MemoryRouter>,
  )
}

function rowOf(table: HTMLElement, name: string) {
  return within(table).getByRole('rowheader', { name }).closest('tr')!
}

describe('SystemPage', () => {
  it('says which services answer', async () => {
    stubApi((url) => (url.pathname === '/api/status' ? { body: status() } : { body: { sources: [] } }))
    renderSystem()

    const services = await screen.findByRole('table', { name: 'Службы' })
    expect(rowOf(services, 'База данных')).toHaveTextContent('Работает')
    expect(rowOf(services, 'Эмбеддер')).toHaveTextContent('Работает · bge-m3')
    expect(rowOf(services, 'Реранкер')).toHaveTextContent('Не отвечает')
    expect(rowOf(services, 'Загрузка файлов')).toHaveTextContent('Выключена')
  })

  it('shows how far indexing has come for each kind of source', async () => {
    stubApi((url) => (url.pathname === '/api/status'
      ? { body: status() }
      : { body: { sources: [book(), book({ path: 'b.pdf', chunks: 6, embedded: 3, quarantined: 3 }), book({ path: 'c.pdf', chunks: 0, embedded: 0, scan_pages: 10, scan_recognised: 2 })] } }))
    renderSystem()

    const indexing = await screen.findByRole('table', { name: 'Индексация' })
    const books = await within(indexing).findByRole('rowheader', { name: 'Книги' }).then((h) => h.closest('tr')!)
    expect(books).toHaveTextContent('2')
    expect(books).toHaveTextContent('8 из 16')
    expect(books).toHaveTextContent('3 фрагмента без вектора')
    expect(books).toHaveTextContent('1 скан ждёт распознавания')
    expect(rowOf(indexing, 'Статьи')).toHaveTextContent('Пусто')
  })

  it('lists the settings the server runs with', async () => {
    stubApi((url) => (url.pathname === '/api/status' ? { body: status() } : { body: { sources: [] } }))
    renderSystem()

    const settings = await screen.findByRole('table', { name: 'Настройки' })
    expect(rowOf(settings, 'model')).toHaveTextContent('bge-m3')
    expect(rowOf(settings, 'ollama')).toHaveTextContent('http://host.docker.internal:11434')
  })

  it('keeps itself current', async () => {
    let embedder = 'unreachable'
    stubApi((url) => (url.pathname === '/api/status' ? { body: status({ embedder }) } : { body: { sources: [] } }))
    renderSystem()

    const services = await screen.findByRole('table', { name: 'Службы' })
    expect(rowOf(services, 'Эмбеддер')).toHaveTextContent('Не отвечает')
    embedder = 'ok'
    expect(await within(services).findByText('Работает · bge-m3')).toBeInTheDocument()
  })

  it('says so when the server does not answer', async () => {
    stubApi(() => ({ status: 502, body: 'bad gateway' }))
    renderSystem()
    expect(await screen.findByRole('alert')).toHaveTextContent('Сервер не отвечает')
  })
})
