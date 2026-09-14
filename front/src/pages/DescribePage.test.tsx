import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { stubApi } from '../test/fetch'
import { DescribePage } from './DescribePage'

function renderDescribe(url = '/describe?kind=book&path=k.pdf') {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <DescribePage />
    </MemoryRouter>,
  )
}

const draft = {
  key: 'h', citekey: 'kleppmann', status: 'draft', updated_at: '',
  csl: { type: 'book', title: 'Designing Data-Intensive Applications', ISBN: '9781449373320', author: [{ family: 'Kleppmann', given: 'Martin' }] },
}

describe('DescribePage', () => {
  it('fills a draft from its ISBN after showing what would change, and saves it as checked', async () => {
    const state: { saved?: unknown } = {}
    const calls = stubApi((url) => {
      if (url.pathname === '/api/bibliography/lookup') {
        return { body: { csl: { type: 'book', title: 'Designing Data-Intensive Applications', publisher: "O'Reilly Media", issued: { 'date-parts': [[2017]] } } } }
      }
      if (url.pathname === '/api/bibliography') return { body: state.saved ?? { key: 'h', reference: draft } }
      return undefined
    })
    const fetchMock = globalThis.fetch as unknown as { mock: { calls: [unknown, RequestInit | undefined][] } }
    renderDescribe()
    const user = userEvent.setup()

    expect(await screen.findByDisplayValue('Designing Data-Intensive Applications')).toBeInTheDocument()
    expect(screen.getByText('kleppmann')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'DOI или ISBN' })).toHaveValue('9781449373320')

    await user.click(screen.getByRole('button', { name: 'Найти' }))
    const table = await screen.findByRole('table')
    expect(within(table).getByText("O'Reilly Media")).toBeInTheDocument()
    expect(within(table).queryByText('Заглавие')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Применить найденное' }))
    expect(screen.getByRole('textbox', { name: 'Издательство' })).toHaveValue("O'Reilly Media")
    expect(screen.getByRole('textbox', { name: 'Год' })).toHaveValue('2017')
    expect(await screen.findByText(/Kleppmann, M\. Designing Data-Intensive Applications/)).toBeInTheDocument()

    state.saved = { ...draft, status: 'checked' }
    await user.click(screen.getByRole('button', { name: 'Сохранить как проверенное' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Сохранено')

    const put = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')!
    const body = JSON.parse(String(put[1]!.body))
    expect(body.status).toBe('checked')
    expect(body.csl.publisher).toBe("O'Reilly Media")
    expect(body.csl.ISBN).toBe('9781449373320')
    expect(calls.some((c) => c.pathname === '/api/bibliography/lookup')).toBe(true)
  })

  it('starts an undescribed manual as a web page and edits names line by line', async () => {
    stubApi(() => ({ body: { key: 'manual:nltk', reference: null } }))
    renderDescribe('/describe?kind=docs&path=nltk/index.html')
    const user = userEvent.setup()

    expect(await screen.findByRole('combobox', { name: 'Тип' })).toHaveValue('webpage')
    await user.type(screen.getByRole('textbox', { name: 'Авторы' }), 'NLTK Project')
    await user.type(screen.getByRole('textbox', { name: 'URL' }), 'https://www.nltk.org/')
    expect(screen.getByRole('textbox', { name: 'Авторы' })).toHaveValue('NLTK Project')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Тип' }), 'article-journal')
    expect(screen.getByRole('textbox', { name: 'Журнал' })).toBeInTheDocument()
  })

  it('explains a lookup that needs the internet', async () => {
    stubApi((url) => (url.pathname === '/api/bibliography/lookup'
      ? { status: 502, body: 'lookup failed' }
      : { body: { key: 'h', reference: draft } }))
    renderDescribe()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Найти' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('только с интернетом')
  })

  it('says when an identifier is unknown', async () => {
    stubApi((url) => (url.pathname === '/api/bibliography/lookup'
      ? { status: 404, body: 'no record' }
      : { body: { key: 'h', reference: draft } }))
    renderDescribe()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Найти' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('не знает такого идентификатора')
  })
})
