import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getReference, styles as fetchStyles, styleXML, type Passage, type Reference } from '../api'
import { bundledStyles, citationItem, describeHref, latex, pandoc, render, type Rendered, type StyleOption } from '../cite'

const remembered = 'corpus.citationStyle'

function rememberedStyle(): string {
  try {
    return localStorage.getItem(remembered) ?? bundledStyles[0].id
  } catch {
    return bundledStyles[0].id
  }
}

// CitationPanel formats the passage for a paper: a bibliography entry and an
// in-text reference in the chosen style, and the key for LaTeX and Pandoc.
export function CitationPanel({ passage }: { passage: Passage }) {
  const [reference, setReference] = useState<Reference | null | undefined>()
  const [options, setOptions] = useState<StyleOption[]>(bundledStyles)
  const [styleId, setStyleId] = useState(rememberedStyle)
  const [rendered, setRendered] = useState<Rendered | { error: string }>()
  const [copied, setCopied] = useState<string | null>(null)

  useEffect(() => {
    const request = new AbortController()
    getReference(passage.kind, passage.path, request.signal)
      .then((r) => setReference(r.reference))
      .catch(() => {
        if (!request.signal.aborted) setReference(null)
      })
    fetchStyles(request.signal)
      .then((added) =>
        setOptions([...bundledStyles, ...added.map((s) => ({ id: `custom:${s.id}`, title: s.title, lang: 'en-US', load: () => styleXML(s.id) }))]),
      )
      .catch(() => {})
    return () => request.abort()
  }, [passage.kind, passage.path])

  const style = options.find((o) => o.id === styleId) ?? options[0]

  useEffect(() => {
    if (!reference) return
    let current = true
    style
      .load()
      .then((xml) => {
        if (current) setRendered(render(xml, style.lang, citationItem(reference, passage)))
      })
      .catch((e: Error) => {
        if (current) setRendered({ error: e.message })
      })
    return () => {
      current = false
    }
  }, [reference, style, passage])

  if (reference === undefined) return null
  if (reference === null) {
    return (
      <section className="citation" aria-label="Цитирование">
        <p className="quiet">
          У источника ещё нет библиографического описания. <Link to={describeHref(passage.kind, passage.path)}>Описать источник</Link>
        </p>
      </section>
    )
  }

  const { locator } = citationItem(reference, passage)
  const copy = async (label: string, text: string) => {
    await navigator.clipboard.writeText(text)
    setCopied(label)
  }
  const choose = (id: string) => {
    setStyleId(id)
    setCopied(null)
    try {
      localStorage.setItem(remembered, id)
    } catch {
      // Private windows refuse storage; the choice then lasts for the page.
    }
  }

  const lines: { label: string; text: string }[] = []
  if (rendered && 'reference' in rendered) {
    lines.push({ label: 'Список литературы', text: rendered.reference }, { label: 'Ссылка в тексте', text: rendered.inText })
  }
  lines.push({ label: 'LaTeX', text: latex(reference.citekey, locator) }, { label: 'Pandoc', text: pandoc(reference.citekey, locator) })

  return (
    <section className="citation" aria-label="Цитирование">
      <div className="citation-bar">
        <label>
          <span>Стиль</span>
          <select value={style.id} onChange={(e) => choose(e.target.value)}>
            {options.map((o) => (
              <option key={o.id} value={o.id}>
                {o.title}
              </option>
            ))}
          </select>
        </label>
        {reference.status === 'draft' && <span className="draft">Описание не проверено</span>}
        <Link to={describeHref(passage.kind, passage.path)}>Изменить описание</Link>
      </div>
      {rendered && 'error' in rendered && (
        <p className="problem" role="alert">
          Стиль не сработал: {rendered.error}
        </p>
      )}
      <dl>
        {lines.map((l) => (
          <div key={l.label} className="citation-line">
            <dt>{l.label}</dt>
            <dd>
              <span>{l.text}</span>
              <button type="button" onClick={() => copy(l.label, l.text)} aria-label={`Скопировать: ${l.label}`}>
                {copied === l.label ? 'Скопировано' : 'Копировать'}
              </button>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
