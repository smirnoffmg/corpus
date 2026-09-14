import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { stubApi } from '../test/fetch'
import { StylesPanel } from './StylesPanel'

describe('StylesPanel', () => {
  it('adds a style by name and lists it', async () => {
    let added = false
    stubApi((url) => {
      if (url.pathname === '/api/styles/fetch') {
        added = true
        return { status: 201, body: { id: 'nature', title: 'Nature' } }
      }
      return { body: { styles: added ? [{ id: 'nature', title: 'Nature' }] : [] } }
    })
    render(<StylesPanel />)
    const user = userEvent.setup()

    await user.type(screen.getByRole('textbox', { name: 'Имя стиля в репозитории CSL' }), 'nature')
    await user.click(screen.getByRole('button', { name: 'Добавить' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Добавлен стиль «Nature»')
    expect(await screen.findByRole('listitem')).toHaveTextContent('Nature')
  })

  it('adds a style from a file and explains refusals', async () => {
    stubApi((url) => {
      if (url.pathname === '/api/styles/fetch') return { status: 502, body: 'offline' }
      if (url.pathname === '/api/styles' && added) return { status: 400, body: 'not a CSL style' }
      return { body: { styles: [] } }
    })
    let added = false
    render(<StylesPanel />)
    const user = userEvent.setup()

    await user.type(screen.getByRole('textbox', { name: 'Имя стиля в репозитории CSL' }), 'nature')
    await user.click(screen.getByRole('button', { name: 'Добавить' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('нужен интернет')

    added = true
    await user.upload(screen.getByLabelText('Файл .csl'), new File(['<html/>'], 'x.csl'))
    expect(await screen.findByText(/Файл не принят/)).toBeInTheDocument()
  })
})
