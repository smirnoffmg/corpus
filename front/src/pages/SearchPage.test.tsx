import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Hit } from '../api'
import { stubApi } from '../test/fetch'
import { SearchPage } from './SearchPage'

const hit: Hit = {
  id: 7, kind: 'docs', title: 'scikit-learn · 1.4. Support Vector Machines', path: 'scikit-learn/modules/svm.html',
  locator: '1.4. Support Vector Machines > 1.4.1. Classification', anchor: 'classification', rank: 0.2,
  snippet: '>>> from sklearn import <<svm>> and <script>x</script>',
}

function Where() {
  const loc = useLocation()
  return <output data-testid="location">{loc.pathname + loc.search}</output>
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/" element={<SearchPage />} />
        <Route path="/read/:id" element={<p>reader</p>} />
      </Routes>
      <Where />
    </MemoryRouter>,
  )
}

describe('SearchPage', () => {
  it('searches what the address says and lists citable hits', async () => {
    const calls = stubApi((url) => (url.pathname === '/api/search' ? { body: { hits: [hit] } } : undefined))
    renderAt('/?q=svm&kind=docs')

    const item = await screen.findByRole('listitem')
    expect(calls[0].search).toBe('?q=svm&kind=docs')
    expect(within(item).getByRole('link', { name: hit.title })).toHaveAttribute('href', '/read/7')
    expect(within(item).getByText(hit.locator)).toBeInTheDocument()
    expect(within(item).getByRole('link', { name: 'Открыть оригинал' })).toHaveAttribute(
      'href', 'http://localhost:8082/docs/scikit-learn/modules/svm.html#classification',
    )
    expect(within(item).getByText('Мануал')).toBeInTheDocument()
    expect(within(item).getByText('svm', { selector: 'mark' })).toBeInTheDocument()
    expect(item.querySelector('script')).toBeNull()
    expect(screen.getByRole('searchbox')).toHaveValue('svm')
    expect(screen.getByRole('radio', { name: 'Мануалы' })).toBeChecked()
  })

  it('puts a new search into the address, so back returns to it', async () => {
    const calls = stubApi(() => ({ body: { hits: [] } }))
    renderAt('/')
    const user = userEvent.setup()

    await user.type(screen.getByRole('searchbox'), 'горутины')
    await user.click(screen.getByRole('radio', { name: 'Точные слова' }))
    await user.click(screen.getByRole('button', { name: 'Найти' }))

    expect(await screen.findByText(/Ничего не нашлось/)).toBeInTheDocument()
    expect(screen.getByTestId('location')).toHaveTextContent('/?q=%D0%B3%D0%BE%D1%80%D1%83%D1%82%D0%B8%D0%BD%D1%8B&mode=fts')
    expect(calls.at(-1)?.searchParams.get('mode')).toBe('fts')
    expect(screen.getByText(/По смыслу/, { selector: 'p' })).toBeInTheDocument()
  })

  it('asks nothing until there is a query', () => {
    const calls = stubApi(() => undefined)
    renderAt('/')
    expect(calls).toHaveLength(0)
    expect(screen.queryByRole('list')).toBeNull()
  })

  it('says what failed when the search does', async () => {
    stubApi(() => ({ status: 500, body: 'connection refused' }))
    renderAt('/?q=svm')
    expect(await screen.findByRole('alert')).toHaveTextContent('connection refused')
  })

  it('focuses the query on / so a search starts from the keyboard', async () => {
    stubApi(() => ({ body: { hits: [] } }))
    renderAt('/')
    const user = userEvent.setup()
    await user.keyboard('/')
    expect(screen.getByRole('searchbox')).toHaveFocus()
  })
})
