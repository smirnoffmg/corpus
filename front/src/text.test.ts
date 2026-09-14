import { describe, expect, it } from 'vitest'
import { blocks, highlightSegments, plural } from './text'

describe('highlightSegments', () => {
  it('splits a ts_headline snippet into marked and plain runs', () => {
    expect(highlightSegments('an <<SVC>> with <<kernel>>')).toEqual([
      { text: 'an ', marked: false },
      { text: 'SVC', marked: true },
      { text: ' with ', marked: false },
      { text: 'kernel', marked: true },
    ])
  })

  it('leaves a Python prompt alone: >>> outside a match is text', () => {
    expect(highlightSegments('>>> from sklearn import <<svm>>')).toEqual([
      { text: '>>> from sklearn import ', marked: false },
      { text: 'svm', marked: true },
    ])
  })

  it('returns plain text unchanged', () => {
    expect(highlightSegments('<script>alert(1)</script>')).toEqual([
      { text: '<script>alert(1)</script>', marked: false },
    ])
  })

  it('returns nothing for an empty snippet', () => {
    expect(highlightSegments('')).toEqual([])
  })
})

describe('blocks', () => {
  it('separates paragraphs and fenced code', () => {
    expect(blocks('First paragraph\nstill first.\n\nSecond.\n\n```\n>>> clf.fit(X)\n\n>>> clf.predict(Y)\n```\n\nAfter.')).toEqual([
      { kind: 'text', content: 'First paragraph\nstill first.' },
      { kind: 'text', content: 'Second.' },
      { kind: 'code', content: '>>> clf.fit(X)\n\n>>> clf.predict(Y)' },
      { kind: 'text', content: 'After.' },
    ])
  })

  it('keeps an unclosed fence as code rather than dropping it', () => {
    expect(blocks('Intro.\n\n```\nx = 1')).toEqual([
      { kind: 'text', content: 'Intro.' },
      { kind: 'code', content: 'x = 1' },
    ])
  })
})

describe('plural', () => {
  const forms: [string, string, string] = ['фрагмент', 'фрагмента', 'фрагментов']
  it.each([
    [1, 'фрагмент'], [2, 'фрагмента'], [4, 'фрагмента'], [5, 'фрагментов'],
    [11, 'фрагментов'], [12, 'фрагментов'], [21, 'фрагмент'], [22, 'фрагмента'], [0, 'фрагментов'], [111, 'фрагментов'],
  ])('%i → %s', (n, want) => {
    expect(plural(n, forms)).toBe(want)
  })
})
