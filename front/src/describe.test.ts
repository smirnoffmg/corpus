import { describe, expect, it } from 'vitest'
import { changes, fieldText, fieldsByType, namesToText, textToNames, withField } from './describe'

const field = (type: string, name: string) => fieldsByType[type].find((f) => f.name === name)!

describe('names', () => {
  it('round-trips people and organisations', () => {
    const names = [{ family: 'Клеппман', given: 'Мартин' }, { literal: 'scikit-learn developers' }]
    expect(namesToText(names)).toBe('Клеппман, Мартин\nscikit-learn developers')
    expect(textToNames('Клеппман, Мартин\n\n scikit-learn developers ')).toEqual(names)
    expect(textToNames('  ')).toBeUndefined()
    expect(namesToText(undefined)).toBe('')
  })
})

describe('fields', () => {
  it('edits a year and a date as CSL date parts', () => {
    let csl = withField({ type: 'book' }, field('book', 'issued'), '2018')
    expect(csl.issued).toEqual({ 'date-parts': [[2018]] })
    expect(fieldText(csl, field('book', 'issued'))).toBe('2018')

    csl = withField(csl, field('webpage', 'accessed'), '2026-09-04')
    expect(csl.accessed).toEqual({ 'date-parts': [[2026, 9, 4]] })
    expect(fieldText(csl, field('webpage', 'accessed'))).toBe('2026-09-04')
  })

  it('keeps text as typed, trailing space included', () => {
    expect(withField({}, field('book', 'publisher'), 'Санкт ').publisher).toBe('Санкт ')
  })

  it('removes a field that is cleared and keeps the ones the form does not show', () => {
    const csl = withField({ type: 'book', ISBN: '1', abstract: 'kept' }, field('book', 'ISBN'), ' ')
    expect(csl).toEqual({ type: 'book', abstract: 'kept' })
  })
})

describe('changes', () => {
  it('lists only the fields a lookup would change', () => {
    const now = { type: 'book', title: 'Designing Data-Intensive Applications', ISBN: '9781449373320' }
    const found = { type: 'book', title: 'Designing Data-Intensive Applications', publisher: "O'Reilly Media", ISBN: '9781449373320', issued: { 'date-parts': [['2017']] } }
    expect(changes(now, found).map((c) => [c.field.name, c.now, c.found])).toEqual([
      ['publisher', '', "O'Reilly Media"],
      ['issued', '', '2017'],
    ])
  })
})
