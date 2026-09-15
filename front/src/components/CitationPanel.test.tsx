import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Passage } from '../api'
import { stubApi } from '../test/fetch'
import { CitationPanel } from './CitationPanel'

const passage: Passage = { kind: 'book', title: 'Высоконагруженные приложения', path: 'k.pdf', locator: 'с. 189 (PDF 203)', page: 203, body: '' }
const reference = {
  key: 'h', citekey: 'kleppman2018', status: 'draft', updated_at: '',
  csl: { type: 'book', title: 'Высоконагруженные приложения', author: [{ family: 'Клеппман', given: 'Мартин' }], publisher: 'Питер', 'publisher-place': 'Санкт-Петербург', issued: { 'date-parts': [[2018]] } },
}

function renderPanel() {
  return render(
    <MemoryRouter>
      <CitationPanel passage={passage} />
    </MemoryRouter>,
  )
}

describe('CitationPanel', () => {
  it('cites the passage in the chosen style, with the printed page', async () => {
    stubApi((url) => {
      if (url.pathname === '/api/bibliography') return { body: { key: 'h', reference } }
      if (url.pathname === '/api/styles') return { body: { styles: [{ id: 'nature', title: 'Nature' }] } }
      if (url.pathname === '/api/styles/nature') return { body: '<style/>' }
      return undefined
    })
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    renderPanel()

    expect(await screen.findByText(/Клеппман, М\. Высоконагруженные приложения/)).toBeInTheDocument()
    expect(screen.getByText('[1, с. 189]')).toBeInTheDocument()
    expect(screen.getByText('\\autocite[189]{kleppman2018}')).toBeInTheDocument()
    expect(screen.getByText('[@kleppman2018, p. 189]')).toBeInTheDocument()
    expect(screen.getByText('Описание не проверено')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'citeproc-js' })).toBeInTheDocument()
    expect(await screen.findByRole('option', { name: 'Nature' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Скопировать: LaTeX' }))
    expect(writeText).toHaveBeenCalledWith('\\autocite[189]{kleppman2018}')

    await user.selectOptions(screen.getByRole('combobox', { name: 'Стиль' }), 'apa')
    expect(await screen.findByText('(Клеппман, 2018, p. 189)')).toBeInTheDocument()
    expect(localStorage.getItem('corpus.citationStyle')).toBe('apa')
    localStorage.clear()
  })

  it('reports a style that cannot format anything', async () => {
    localStorage.setItem('corpus.citationStyle', 'custom:broken')
    stubApi((url) => {
      if (url.pathname === '/api/bibliography') return { body: { key: 'h', reference } }
      if (url.pathname === '/api/styles') return { body: { styles: [{ id: 'broken', title: 'Broken' }] } }
      if (url.pathname === '/api/styles/broken') return { status: 404, body: 'gone' }
      return undefined
    })
    renderPanel()
    expect(await screen.findByRole('alert')).toHaveTextContent('Стиль не сработал')
    localStorage.clear()
  })

  it('offers to describe a source that has no description', async () => {
    stubApi((url) => (url.pathname === '/api/bibliography' ? { body: { key: 'h', reference: null } } : { body: { styles: [] } }))
    renderPanel()
    expect(await screen.findByRole('link', { name: 'Описать источник' })).toHaveAttribute('href', '/describe?kind=book&path=k.pdf')
  })
})
