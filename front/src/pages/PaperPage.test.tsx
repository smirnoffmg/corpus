import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Citation } from '../api'
import { stubApi } from '../test/fetch'
import { PaperPage } from './PaperPage'

const path = 'uploads/sculley2015.pdf'

const source = {
  kind: 'paper', path, title: 'Hidden Technical Debt in Machine Learning Systems', indexed_at: '2026-09-16T08:00:00Z',
  chunks: 8, embedded: 8, quarantined: 0, description: 'checked',
}

const reference = {
  key: 'h', citekey: 'sculley2015', status: 'checked', updated_at: '',
  csl: {
    type: 'paper-conference', title: 'Hidden Technical Debt in Machine Learning Systems',
    author: [{ family: 'Sculley', given: 'D.' }, { family: 'Holt', given: 'Gary' }],
    'container-title': 'Advances in Neural Information Processing Systems', issued: { 'date-parts': [[2015]] },
    DOI: '10.5555/2969442.2969519',
  },
}

const thin: Citation = { ord: 1, raw: '[1] R. Ananthanarayanan et al. Photon. SIGMOD 2013. doi:10.1145/2463676.2465272', doi: '10.1145/2463676.2465272' }
const filled: Citation = { ...thin, title: 'Photon: fault-tolerant and scalable joining of continuous data streams', year: 2013 }

function renderPaper(answer: (url: URL, method?: string) => { status?: number; body: unknown } | undefined) {
  const calls = stubApi((url) => answer(url))
  render(
    <MemoryRouter initialEntries={[`/paper?path=${encodeURIComponent(path)}`]}>
      <Routes>
        <Route path="/paper" element={<PaperPage />} />
      </Routes>
    </MemoryRouter>,
  )
  return calls
}

function library(url: URL) {
  if (url.pathname === '/api/sources') return { body: { sources: [source] } }
  if (url.pathname === '/api/bibliography') return { body: { key: 'h', reference } }
  if (url.pathname === '/api/citations/citing') return { body: { citing: [{ path: 'uploads/breck2017.pdf', title: 'The ML Test Score', citation: { ord: 3, raw: '[3] D. Sculley et al.' } }] } }
  return undefined
}

describe('PaperPage', () => {
  it('shows what the publication is, where its PDF is, and how it is described', async () => {
    const calls = renderPaper((url) => library(url) ?? (url.pathname === '/api/citations' ? { body: { path, citations: [] } } : undefined))

    expect(await screen.findByRole('heading', { level: 1, name: source.title })).toBeInTheDocument()
    expect(calls.find((c) => c.pathname === '/api/sources')?.searchParams.get('prefix')).toBe(path)
    expect(screen.getByText('Sculley, D.; Holt, Gary')).toBeInTheDocument()
    expect(screen.getByText('Advances in Neural Information Processing Systems, 2015')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '10.5555/2969442.2969519' })).toHaveAttribute('href', 'https://doi.org/10.5555/2969442.2969519')
    expect(screen.getByRole('link', { name: 'Открыть PDF' })).toHaveAttribute('href', 'http://localhost:8082/papers/uploads/sculley2015.pdf')
    expect(screen.getByRole('link', { name: 'Изменить описание' })).toHaveAttribute('href', '/describe?kind=paper&path=uploads%2Fsculley2015.pdf')
    expect(screen.getByText('Проверено')).toBeInTheDocument()
  })

  it('shows its references and the publications that cite it', async () => {
    renderPaper((url) => library(url) ?? (url.pathname === '/api/citations' ? { body: { path, citations: [thin] } } : undefined))

    const refs = await screen.findByRole('region', { name: /Ссылается на/ })
    expect(within(refs).getByText(thin.raw)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'The ML Test Score' })).toHaveAttribute('href', '/paper?path=uploads%2Fbreck2017.pdf')
  })

  it('fills thin references in from doi.org on request, and says how many', async () => {
    let enriched = false
    const calls = renderPaper((url) => {
      if (url.pathname === '/api/citations/enrich') {
        enriched = true
        return { body: { asked: 1, filled: 1, citations: [filled] } }
      }
      if (url.pathname === '/api/citations') return { body: { path, citations: [enriched ? filled : thin] } }
      return library(url)
    })
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Дополнить по DOI' }))

    expect(calls.some((c) => c.pathname === '/api/citations/enrich' && c.searchParams.get('path') === path)).toBe(true)
    expect(await screen.findByText('Дополнено записей: 1 из 1 запрошенных.')).toBeInTheDocument()
    expect(await screen.findByText(filled.title!)).toBeInTheDocument()
  })

  it('says when there is no such publication', async () => {
    renderPaper((url) => {
      if (url.pathname === '/api/sources') return { body: { sources: [] } }
      if (url.pathname === '/api/bibliography') return { status: 404, body: 'no such source' }
      if (url.pathname.startsWith('/api/citations')) return { status: 404, body: 'nothing is indexed' }
      return undefined
    })

    expect(await screen.findByRole('alert')).toHaveTextContent('Такой статьи в библиотеке нет')
  })
})
