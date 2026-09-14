import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router'
import { search, type Hit, type Kind, type Mode, type Status } from '../api'
import { Highlight } from '../components/Highlight'
import { kindFilters, kindName, modes } from '../kinds'
import { originalUrl } from '../library'
import { plural } from '../text'

interface Result {
  key: string
  hits?: Hit[]
  error?: string
}

export function SearchPage({ status }: { status?: Status | null }) {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const kind = (params.get('kind') ?? '') as Kind | ''
  const mode = (params.get('mode') ?? 'hybrid') as Mode
  const key = params.toString()

  const [result, setResult] = useState<Result>()
  const input = useRef<HTMLInputElement>(null)
  const form = useRef<HTMLFormElement>(null)

  useEffect(() => {
    if (!q) return
    const request = new AbortController()
    search({ q, kind, mode }, request.signal)
      .then((hits) => setResult({ key, hits }))
      .catch((e: Error) => {
        if (!request.signal.aborted) setResult({ key, error: e.message })
      })
    return () => request.abort()
  }, [key, q, kind, mode])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement
      if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) {
        e.preventDefault()
        input.current?.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const data = new FormData(form.current!)
    const next = new URLSearchParams()
    const text = String(data.get('q') ?? '').trim()
    if (text) next.set('q', text)
    if (data.get('kind')) next.set('kind', String(data.get('kind')))
    if (data.get('mode') && data.get('mode') !== 'hybrid') next.set('mode', String(data.get('mode')))
    setParams(next)
  }

  // A filter changed with a query already typed means "the same search, but
  // narrower": run it rather than wait for another press of the button.
  const refine = () => {
    if (input.current?.value.trim()) submit()
  }

  const current = result?.key === key ? result : undefined

  return (
    <div className="search">
      {/* The form is keyed by the address so that back and forward restore
          what was searched, not what was last typed. */}
      <form ref={form} key={key} className="query" role="search" onSubmit={submit}>
        <div className="query-line">
          <input
            ref={input}
            type="search"
            name="q"
            defaultValue={q}
            placeholder="Что найти?"
            aria-label="Запрос"
            autoFocus={!q}
          />
          <button type="submit">Найти</button>
        </div>
        <div className="filters">
          <fieldset>
            <legend>Где</legend>
            {kindFilters.map((f) => (
              <label key={f.value} className="choice">
                <input type="radio" name="kind" value={f.value} defaultChecked={kind === f.value} onChange={refine} />
                <span>{f.label}</span>
              </label>
            ))}
          </fieldset>
          <fieldset>
            <legend>Как</legend>
            {modes.map((m) => (
              <label key={m.value} className="choice" title={m.hint}>
                <input type="radio" name="mode" value={m.value} defaultChecked={mode === m.value} onChange={refine} />
                <span>{m.label}</span>
              </label>
            ))}
          </fieldset>
        </div>
      </form>

      {!q && status && (
        <p className="quiet">
          В корпусе {status.sources.toLocaleString('ru-RU')} {plural(status.sources, ['источник', 'источника', 'источников'])} и{' '}
          {status.chunks.toLocaleString('ru-RU')} {plural(status.chunks, ['фрагмент', 'фрагмента', 'фрагментов'])}.
        </p>
      )}
      {q && !current && <p className="quiet" aria-live="polite">Ищу…</p>}
      {current?.error && (
        <p className="problem" role="alert">
          Поиск не ответил: {current.error}. Проверьте, что сервер запущен: <code>docker compose ps</code>.
        </p>
      )}
      {current?.hits && current.hits.length === 0 && (
        <div className="empty">
          <p>Ничего не нашлось по запросу «{q}».</p>
          {mode === 'fts' ? (
            <p>
              Точные слова ищутся внутри одного языка и все сразу. По смыслу находит и пересказ, и текст на другом языке.
            </p>
          ) : (
            <p>Попробуйте другие слова или уберите ограничение по типу источника.</p>
          )}
        </div>
      )}
      {current?.hits && current.hits.length > 0 && (
        <ol className="hits">
          {current.hits.map((h) => {
            const original = originalUrl(h)
            return (
            <li key={h.id} className={`hit kind-${h.kind}`}>
              <div className="slip">
                <Link to={`/read/${h.id}`} className="slip-title">
                  {h.title}
                </Link>
                <span className="slip-locator">{h.locator}</span>
                <span className="slip-kind">
                  {kindName[h.kind]}
                  {original && (
                    <a href={original} target="_blank" rel="noreferrer" className="slip-open">
                      Открыть оригинал
                    </a>
                  )}
                </span>
              </div>
              <p className="snippet">
                <Highlight text={h.snippet} />
              </p>
            </li>
            )
          })}
        </ol>
      )}
    </div>
  )
}
