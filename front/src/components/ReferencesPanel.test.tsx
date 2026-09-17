import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Citation, CitingPaper } from '../api'
import { stubApi } from '../test/fetch'
import { ReferencesPanel } from './ReferencesPanel'

const cited: Citation[] = [
  {
    ord: 1,
    raw: '[1] M. Kleppmann. Designing Data-Intensive Applications. O’Reilly, 2017.',
    label: '[1]',
    title: 'Designing Data-Intensive Applications',
    year: 2017,
    resolved: 'bk',
    resolved_kind: 'book',
    resolved_path: 'uploads/kleppmann.pdf',
    resolved_title: 'Высоконагруженные приложения',
  },
  { ord: 2, raw: '[2] L. Lamport. Time, clocks. CACM, 1978.', label: '[2]', doi: '10.1145/359545.359563' },
]

const citing: CitingPaper[] = [
  { path: 'other.pdf', title: 'Другая статья', citation: { ord: 4, raw: '[4] Dean, Ghemawat. MapReduce.' } },
]

function renderPanel(answers: { cited?: Citation[]; citing?: CitingPaper[] }) {
  stubApi((url) => {
    if (url.pathname === '/api/citations') return { body: { path: 'mapreduce.pdf', citations: answers.cited ?? [] } }
    if (url.pathname === '/api/citations/citing') return { body: { citing: answers.citing ?? [] } }
    return undefined
  })
  return render(
    <MemoryRouter>
      <ReferencesPanel path="mapreduce.pdf" />
    </MemoryRouter>,
  )
}

describe('ReferencesPanel', () => {
  it('lists what the publication cites, as printed', async () => {
    renderPanel({ cited })
    expect(await screen.findByText(/Designing Data-Intensive Applications\. O’Reilly, 2017\./)).toBeInTheDocument()
    expect(screen.getByText(/L\. Lamport\. Time, clocks/)).toBeInTheDocument()
  })

  it('shows what was read out of an entry beneath it', async () => {
    renderPanel({ cited })
    expect(await screen.findByText('Designing Data-Intensive Applications', { selector: 'cite' })).toBeInTheDocument()
    expect(screen.getByText(/· 2017$/)).toBeInTheDocument()
  })

  it('links a citation to the source the library already holds', async () => {
    renderPanel({ cited })
    const link = await screen.findByRole('link', { name: /Высоконагруженные приложения/ })
    expect(link).toHaveAttribute('href', '/library?kind=book&filter=uploads%2Fkleppmann.pdf')
  })

  it('opens the card of a cited work that is a publication in the library', async () => {
    renderPanel({ cited: [{ ord: 1, raw: '[1] D. Sculley et al. Hidden technical debt.', resolved: 'p', resolved_kind: 'paper', resolved_path: 'uploads/sculley2015.pdf', resolved_title: 'Hidden Technical Debt' }] })
    expect(await screen.findByRole('link', { name: 'Hidden Technical Debt' })).toHaveAttribute('href', '/paper?path=uploads%2Fsculley2015.pdf')
  })

  it('offers doi.org for a work the library does not hold', async () => {
    renderPanel({ cited })
    expect(await screen.findByRole('link', { name: '10.1145/359545.359563' })).toHaveAttribute(
      'href',
      'https://doi.org/10.1145/359545.359563',
    )
  })

  it('shows which publications cite this one', async () => {
    renderPanel({ cited, citing })
    expect(await screen.findByRole('link', { name: 'Другая статья' })).toHaveAttribute('href', '/paper?path=other.pdf')
  })

  it('counts the publications that cite, not their entries', async () => {
    renderPanel({ citing: [...citing, { path: 'other.pdf', title: 'Другая статья', citation: { ord: 9, raw: '[9] The same work again.' } }] })
    expect(await screen.findByRole('heading', { name: 'На эту работу ссылаются: 1 публикация' })).toBeInTheDocument()
  })

  it('says plainly when nothing was read out of the paper', async () => {
    renderPanel({})
    expect(await screen.findByText(/Список литературы не распознан/)).toBeInTheDocument()
  })
})
