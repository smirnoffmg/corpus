import { describe, expect, it } from 'vitest'
import type { Passage, Reference } from './api'
import { bundledStyles, citationItem, pandoc, latex, printedPage, render } from './cite'

const kleppmann: Reference = {
  key: 'h', citekey: 'kleppman2018', status: 'checked', updated_at: '',
  csl: {
    type: 'book', title: 'Высоконагруженные приложения', author: [{ family: 'Клеппман', given: 'Мартин' }],
    publisher: 'Питер', 'publisher-place': 'Санкт-Петербург', issued: { 'date-parts': [[2018]] }, 'number-of-pages': '640',
  },
}

const bookPassage: Passage = { kind: 'book', title: 'Высоконагруженные приложения', path: 'k.pdf', locator: 'с. 189 (PDF 203)', page: 203, body: '' }

describe('render', () => {
  it('formats a book after ГОСТ Р 7.0.100–2018 with the page cited', async () => {
    const gost = await bundledStyles[0].load()
    const out = render(gost, 'ru-RU', citationItem(kleppmann, bookPassage))
    // citeproc keeps initials and "640 с." together with non-breaking spaces.
    expect(out.reference.replace(/\s/g, ' ')).toBe(
      'Клеппман, М. Высоконагруженные приложения / М. Клеппман. – Санкт-Петербург : Питер, 2018. – 640 с. – Текст : непосредственный.',
    )
    expect(out.inText).toBe('[1, с. 189]')
  })

  it('formats the same book in APA', async () => {
    const apa = await bundledStyles.find((s) => s.id === 'apa')!.load()
    const out = render(apa, 'en-US', citationItem(kleppmann, bookPassage))
    expect(out.reference).toContain('Клеппман, М. (2018).')
    expect(out.inText).toBe('(Клеппман, 2018, p. 189)')
  })
})

describe('citationItem', () => {
  it('cites a manual page as a web page of the manual', () => {
    const manual: Reference = {
      key: 'manual:scikit-learn', citekey: 'scikitlearn', status: 'draft', updated_at: '',
      csl: { type: 'webpage', title: 'scikit-learn 1.9.1 documentation', URL: 'https://scikit-learn.org/stable/', publisher: 'scikit-learn developers', accessed: { 'date-parts': [[2026, 9, 14]] } },
    }
    const page: Passage = { kind: 'docs', title: 'scikit-learn · 1.4. Support Vector Machines', path: 'scikit-learn/modules/svm.html', locator: '1.4. SVM > 1.4.1. Classification', anchor: 'classification', body: '' }
    const { item } = citationItem(manual, page)
    expect(item).toMatchObject({
      type: 'webpage', title: '1.4. Support Vector Machines', 'container-title': 'scikit-learn 1.9.1 documentation',
      URL: 'https://scikit-learn.org/stable/modules/svm.html#classification', publisher: 'scikit-learn developers',
    })
  })
})

describe('printedPage', () => {
  it.each([
    ['с. 189 (PDF 203)', '189'],
    ['с. 58', '58'],
    ['PDF 256', undefined],
    ['Что это > Пример', undefined],
  ])('%s → %s', (locator, want) => {
    expect(printedPage(locator)).toBe(want)
  })
})

describe('keys for writing tools', () => {
  it('cites with the page when there is one', () => {
    expect(latex('kleppman2018', '189')).toBe('\\autocite[189]{kleppman2018}')
    expect(latex('kleppman2018')).toBe('\\autocite{kleppman2018}')
    expect(pandoc('kleppman2018', '189')).toBe('[@kleppman2018, p. 189]')
    expect(pandoc('kleppman2018')).toBe('[@kleppman2018]')
  })
})
