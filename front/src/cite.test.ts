import { describe, expect, it } from 'vitest'
import type { Passage, Reference } from './api'
import { bundledStyles, citationItem, pandoc, latex, printedPage, render, titleLanguage } from './cite'

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
    // One passage has no place in a list, so its number is left to the list.
    expect(out.inText).toBe('[N, с. 189]')
    expect(out.numbered).toBe(true)
  })

  // ГОСТ Р 7.0.100–2018, 4.8: the description is in the language of the
  // resource — «30 p.» (с. 81), «P. 18–30» (с. 99).
  it('gives the extent of an English book in English', async () => {
    const gost = await bundledStyles[0].load()
    const book: Reference = {
      key: 'b', citekey: 'butcher2016', status: 'draft', updated_at: '',
      csl: {
        type: 'book', title: 'Go in Practice', author: [{ family: 'Butcher', given: 'Matt' }],
        publisher: 'Manning', issued: { 'date-parts': [[2016]] }, 'number-of-pages': '312',
      },
    }
    const out = render(gost, 'ru-RU', citationItem(book, { ...bookPassage, locator: 'с. 141 (PDF 164)' }))
    expect(out.reference.replace(/\s/g, ' ')).toBe(
      'Butcher, M. Go in Practice / M. Butcher. – Manning, 2016. – 312 p. – Текст : непосредственный.',
    )
  })

  it('gives the pages of an English article in English', async () => {
    const gost = await bundledStyles[0].load()
    const article: Reference = {
      key: 'a', citekey: 'codd1970', status: 'draft', updated_at: '',
      csl: {
        type: 'article-journal', title: 'A Relational Model of Data', 'container-title': 'Communications of the ACM',
        issued: { 'date-parts': [[1970]] }, page: '377-387',
      },
    }
    const out = render(gost, 'ru-RU', citationItem(article, { ...bookPassage, kind: 'paper', locator: 'PDF 1' }))
    expect(out.reference.replace(/\s/g, ' ')).toContain('– P. 377–387')
  })

  // Only English has a layout of its own; any other language keeps the
  // Russian terms rather than failing.
  it('gives the extent of a book in another language in Russian', async () => {
    const gost = await bundledStyles[0].load()
    const book: Reference = { key: 'd', citekey: 'd', status: 'checked', updated_at: '', csl: { type: 'book', title: 'Einführung', language: 'de', 'number-of-pages': '100' } }
    const out = render(gost, 'ru-RU', citationItem(book, bookPassage))
    expect(out.reference.replace(/\s/g, ' ')).toContain('– 100 с.')
  })

  it('formats the same book in APA', async () => {
    const apa = await bundledStyles.find((s) => s.id === 'apa')!.load()
    const out = render(apa, 'en-US', citationItem(kleppmann, bookPassage))
    expect(out.reference).toContain('Клеппман, М. (2018).')
    expect(out.inText).toBe('(Клеппман, 2018, p. 189)')
    expect(out.numbered).toBe(false)
  })

  it('leaves the number to the list in IEEE too', async () => {
    const ieee = await bundledStyles.find((s) => s.id === 'ieee')!.load()
    const out = render(ieee, 'en-US', citationItem(kleppmann, { ...bookPassage, locator: 'с. 1 (PDF 15)' }))
    expect(out.inText).toBe('[N, p. 1]')
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

describe('titleLanguage', () => {
  it.each([
    ['Высоконагруженные приложения', 'ru'],
    ['Go in Practice: Includes 70 Techniques', 'en'],
    ['Git. Практическое руководство. Управление и контроль версий', 'ru'],
    // A shelf tag in Russian does not make an English paper Russian.
    ['Attention Is All You Need (Vaswani, трансформер)', 'en'],
    ['', 'en'],
  ])('%s → %s', (title, want) => {
    expect(titleLanguage(title)).toBe(want)
  })

  it('leaves a language the description states', () => {
    const ref: Reference = { key: 'x', citekey: 'x', status: 'checked', updated_at: '', csl: { type: 'book', title: 'Go in Practice', language: 'de' } }
    expect(citationItem(ref, bookPassage).item.language).toBe('de')
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
