import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, getReference, lookupReference, saveReference, type CSLRecord, type Kind, type Reference } from '../api'
import { bundledStyles, render } from '../cite'
import { changes, fieldText, fieldsByType, languages, types, withField } from '../describe'

function lookupProblem(e: unknown): string {
  if (e instanceof ApiError && e.status === 404) return 'Сервис не знает такого идентификатора. Проверьте его или заполните описание вручную.'
  if (e instanceof ApiError && e.status === 502) return 'Не удалось связаться с сервисом: поиск по DOI и ISBN работает только с интернетом.'
  return `Поиск не удался: ${e instanceof Error ? e.message : String(e)}`
}

export function DescribePage() {
  const [params] = useSearchParams()
  const kind = (params.get('kind') ?? 'book') as Kind
  const path = params.get('path') ?? ''

  const [loaded, setLoaded] = useState<{ reference: Reference | null; error?: string }>()
  const [csl, setCSL] = useState<CSLRecord>({})
  const [identifier, setIdentifier] = useState('')
  const [found, setFound] = useState<CSLRecord | null>(null)
  const [problem, setProblem] = useState<string | null>(null)
  const [saved, setSaved] = useState<Reference | null>(null)
  const [preview, setPreview] = useState('')
  // Names are kept as typed while being edited: parsed on every keystroke, a
  // line cannot hold the space between a name and the next word.
  const [namesText, setNamesText] = useState<Record<string, string>>({})

  useEffect(() => {
    const request = new AbortController()
    getReference(kind, path, request.signal)
      .then(({ reference }) => {
        setLoaded({ reference })
        const record = reference?.csl ?? { type: kind === 'docs' ? 'webpage' : kind === 'paper' ? 'article-journal' : 'book' }
        setCSL(record)
        setIdentifier(String(record.DOI ?? record.ISBN ?? ''))
      })
      .catch((e: Error) => {
        if (!request.signal.aborted) setLoaded({ reference: null, error: e.message })
      })
    return () => request.abort()
  }, [kind, path])

  useEffect(() => {
    let current = true
    bundledStyles[0]
      .load()
      .then((xml) => {
        if (current) setPreview(csl.title ? render(xml, 'ru-RU', { item: { ...csl, id: 'preview' } }).reference : '')
      })
      .catch(() => {})
    return () => {
      current = false
    }
  }, [csl])

  if (!loaded) return <p className="quiet">Открываю описание…</p>
  if (loaded.error) {
    return (
      <p className="problem" role="alert">
        Описание не открылось: {loaded.error}
      </p>
    )
  }

  const type = String(csl.type ?? 'book')
  const fields = fieldsByType[type] ?? fieldsByType.book
  const reference = saved ?? loaded.reference

  const lookup = async () => {
    setProblem(null)
    setFound(null)
    const id = identifier.trim()
    try {
      setFound(await lookupReference(id.startsWith('10.') ? { doi: id } : { isbn: id }))
    } catch (e) {
      setProblem(lookupProblem(e))
    }
  }

  const apply = () => {
    if (!found) return
    setNamesText({})
    setCSL({ ...csl, ...found })
    setFound(null)
  }

  const save = async (status: Reference['status']) => {
    setProblem(null)
    try {
      setSaved(await saveReference(kind, path, csl, status))
    } catch (e) {
      setProblem(`Не сохранилось: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const proposed = found ? changes(csl, found) : []

  return (
    <div className="describe">
      <Link to="/library" className="back">
        К библиотеке
      </Link>
      <h1>Описание источника</h1>
      <p className="quiet describe-path">{path}</p>
      {reference && (
        <p className="describe-status">
          Ключ для цитирования <code>{reference.citekey}</code>, {reference.status === 'checked' ? 'описание проверено' : 'черновик'}
        </p>
      )}

      <section className="lookup" aria-labelledby="lookup-title">
        <h2 id="lookup-title">Заполнить по DOI или ISBN</h2>
        <div className="lookup-line">
          <input aria-label="DOI или ISBN" value={identifier} onChange={(e) => setIdentifier(e.target.value)} placeholder="10.1145/… или 978-5-…" />
          <button type="button" onClick={lookup} disabled={!identifier.trim()}>
            Найти
          </button>
        </div>
        {found && (
          <div className="found">
            {proposed.length === 0 ? (
              <p>Найденная запись ничего не меняет в описании.</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th scope="col">Поле</th>
                    <th scope="col">Сейчас</th>
                    <th scope="col">Найдено</th>
                  </tr>
                </thead>
                <tbody>
                  {proposed.map((c) => (
                    <tr key={c.field.name}>
                      <td>{c.field.label}</td>
                      <td className="detail">{c.now || '—'}</td>
                      <td>{c.found}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <div className="actions">
              <button type="button" onClick={apply} disabled={proposed.length === 0}>
                Применить найденное
              </button>
              <button type="button" onClick={() => setFound(null)}>
                Отменить
              </button>
            </div>
          </div>
        )}
      </section>

      <form className="describe-form" onSubmit={(e) => e.preventDefault()}>
        <div className="field">
          <label htmlFor="csl-type">Тип</label>
          <select id="csl-type" value={type} onChange={(e) => setCSL({ ...csl, type: e.target.value })}>
            {types.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </div>
        {fields.map((field) => {
          const id = `csl-${field.name}`
          const value = field.kind === 'names' ? (namesText[field.name] ?? fieldText(csl, field)) : fieldText(csl, field)
          return (
            <div className="field" key={field.name}>
              <label htmlFor={id}>{field.label}</label>
              {field.kind === 'names' ? (
                <>
                  <textarea id={id} rows={Math.max(2, value.split('\n').length + 1)} value={value} onChange={(e) => {
                      setNamesText({ ...namesText, [field.name]: e.target.value })
                      setCSL(withField(csl, field, e.target.value))
                    }}
                    aria-describedby={`${id}-hint`} />
                  <small id={`${id}-hint`}>По одному на строку: «Фамилия, Имя». Организацию — без запятой.</small>
                </>
              ) : (
                <input id={id} type={field.kind === 'date' ? 'date' : 'text'} value={value} onChange={(e) => setCSL(withField(csl, field, e.target.value))} />
              )}
            </div>
          )
        })}
        <div className="field">
          <label htmlFor="csl-language">Язык</label>
          <select id="csl-language" value={String(csl.language ?? '')} onChange={(e) => setCSL(withField(csl, { name: 'language', label: '' }, e.target.value))}>
            {languages.map((l) => (
              <option key={l.value} value={l.value}>
                {l.label}
              </option>
            ))}
          </select>
        </div>
      </form>

      {preview && (
        <section className="preview" aria-label="Как это выглядит по ГОСТ">
          <p className="quiet">Так это выглядит по ГОСТ Р 7.0.100–2018:</p>
          <p className="preview-text">{preview}</p>
        </section>
      )}

      {problem && (
        <p className="problem" role="alert">
          {problem}
        </p>
      )}
      <div className="upload-actions">
        <button type="button" onClick={() => save('checked')}>
          Сохранить как проверенное
        </button>
        <button type="button" className="secondary" onClick={() => save('draft')}>
          Сохранить черновик
        </button>
        {saved && <span className="quiet" role="status">Сохранено</span>}
      </div>
    </div>
  )
}
