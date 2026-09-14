import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Passage } from '../api'
import { stubApi } from '../test/fetch'
import { ReaderPage } from './ReaderPage'

const passage: Passage = {
  kind: 'docs', title: 'scikit-learn · 1.4. Support Vector Machines', path: 'scikit-learn/modules/svm.html',
  locator: '1.4. Support Vector Machines > 1.4.1. Classification', anchor: 'classification',
  body: 'SVC is a class capable of classification.\n\n```\n>>> clf = svm.SVC()\n```',
  previous: 'The advantages of support vector machines are:', next: 'Multi-class classification follows.',
}

function renderReader(id = '7') {
  return render(
    <MemoryRouter initialEntries={[`/read/${id}`]}>
      <Routes>
        <Route path="/read/:id" element={<ReaderPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('ReaderPage', () => {
  it('shows the passage, its citation and a link to the original section', async () => {
    const calls = stubApi(() => ({ body: passage }))
    renderReader()

    expect(await screen.findByRole('heading', { name: passage.title })).toBeInTheDocument()
    expect(calls[0].pathname + calls[0].search).toBe('/api/read?id=7&neighbours')
    expect(screen.getByText(passage.locator)).toBeInTheDocument()
    expect(screen.getByText('>>> clf = svm.SVC()', { selector: 'pre code' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Открыть оригинал' })).toHaveAttribute(
      'href', '/docs/scikit-learn/modules/svm.html#classification',
    )
    expect(screen.getByText(passage.previous!)).toBeInTheDocument()
    expect(screen.getByText(passage.next!)).toBeInTheDocument()
  })

  it('copies the citation and opens a book at its page', async () => {
    stubApi(() => ({ body: { ...passage, kind: 'book', path: 'Concurrency in Go.pdf', title: 'Concurrency in Go', locator: 'с. 189 (PDF 203)', page: 203, anchor: undefined } }))
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    renderReader()

    await user.click(await screen.findByRole('button', { name: 'Скопировать цитату' }))
    expect(writeText).toHaveBeenCalledWith('Concurrency in Go, с. 189 (PDF 203)')
    expect(await screen.findByRole('button', { name: 'Скопировано' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Открыть оригинал' })).toHaveAttribute('href', '/books/Concurrency%20in%20Go.pdf#page=203')
  })

  it('offers no original for a note', async () => {
    stubApi(() => ({ body: { ...passage, kind: 'vault', path: 'note.md', anchor: undefined } }))
    renderReader()
    await screen.findByRole('heading', { name: passage.title })
    expect(screen.queryByRole('link', { name: 'Открыть оригинал' })).toBeNull()
  })

  it('explains a passage that is gone', async () => {
    stubApi(() => ({ status: 404, body: 'read chunk 7: no rows in result set' }))
    renderReader()
    expect(await screen.findByRole('alert')).toHaveTextContent(/больше нет.*Повторите поиск/)
  })
})
