import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { App } from './App'
import { stubApi } from './test/fetch'

function renderApp(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <App />
    </MemoryRouter>,
  )
}

describe('App', () => {
  it('warns that search runs on words alone while the embedder is away', async () => {
    stubApi((url) => (url.pathname === '/api/status'
      ? { body: { sources: 4410, chunks: 65810, db: 'ok', embedder: 'unreachable', degraded: '…' } }
      : undefined))
    renderApp('/')
    expect(await screen.findByRole('status')).toHaveTextContent('Поиск по смыслу недоступен')
    expect(screen.getByText(/4\s410 источников/)).toBeInTheDocument()
  })

  it('lets a note be opened in the vault the server names', async () => {
    stubApi((url) => {
      if (url.pathname === '/api/status') return { body: { sources: 1, chunks: 1, db: 'ok', embedder: 'ok', vault: 'obsidian' } }
      return { body: { hits: [{ id: 3, kind: 'vault', title: 'Кросс-энтропия', path: 'Кросс-энтропия.md', locator: '', rank: 1, snippet: 'x' }] } }
    })
    renderApp('/?q=x')
    expect(await screen.findByRole('link', { name: 'Открыть в Obsidian' })).toHaveAttribute(
      'href', 'obsidian://open?vault=obsidian&file=' + encodeURIComponent('Кросс-энтропия'),
    )
  })

  it('stays quiet when everything answers', async () => {
    stubApi(() => ({ body: { sources: 1, chunks: 2, db: 'ok', embedder: 'ok' } }))
    renderApp('/')
    expect(await screen.findByText(/1 источник/)).toBeInTheDocument()
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('routes to the library and marks it current', async () => {
    stubApi((url) => (url.pathname === '/api/status'
      ? { body: { sources: 1, chunks: 2, db: 'ok', embedder: 'ok' } }
      : { body: { sources: [] } }))
    renderApp('/library')
    expect(await screen.findByRole('heading', { name: 'Библиотека' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Библиотека' })).toHaveAttribute('aria-current', 'page')
  })

  it('has a way back from an address that leads nowhere', async () => {
    stubApi(() => ({ body: { sources: 1, chunks: 2, db: 'ok', embedder: 'ok' } }))
    renderApp('/nowhere')
    expect(screen.getByRole('link', { name: 'Перейти к поиску' })).toHaveAttribute('href', '/')
  })
})
