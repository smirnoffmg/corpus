import { useEffect, useState } from 'react'
import { addStyle, fetchStyle, styles as fetchStyles, ApiError, type StyleInfo } from '../api'

function refusal(e: unknown): string {
  if (e instanceof ApiError && e.status === 404) return 'В репозитории CSL нет стиля с таким именем. Имя — это имя файла без .csl, например nature.'
  if (e instanceof ApiError && e.status === 502) return 'Не удалось связаться с репозиторием стилей: нужен интернет. Можно загрузить файл .csl.'
  if (e instanceof ApiError && e.status === 400) return `Файл не принят: ${e.message}`
  return `Стиль не добавлен: ${e instanceof Error ? e.message : String(e)}`
}

// StylesPanel adds a journal's citation style, by its name in the CSL
// repository or from a .csl file; once added it works offline.
export function StylesPanel() {
  const [list, setList] = useState<StyleInfo[]>([])
  const [name, setName] = useState('')
  const [problem, setProblem] = useState<string | null>(null)
  const [added, setAdded] = useState<string | null>(null)

  useEffect(() => {
    const request = new AbortController()
    fetchStyles(request.signal)
      .then(setList)
      .catch(() => {})
    return () => request.abort()
  }, [added])

  const done = (style: StyleInfo) => {
    setAdded(style.title)
    setProblem(null)
    setName('')
  }

  const byName = async () => {
    try {
      done(await fetchStyle(name.trim()))
    } catch (e) {
      setProblem(refusal(e))
    }
  }

  const fromFile = async (file: File | undefined) => {
    if (!file) return
    try {
      done(await addStyle(await file.text()))
    } catch (e) {
      setProblem(refusal(e))
    }
  }

  return (
    <section className="styles" aria-labelledby="styles-title">
      <h2 id="styles-title">Стили цитирования</h2>
      <p className="quiet">Встроены ГОСТ Р 7.0.100–2018, APA и IEEE. Стиль журнала можно добавить из репозитория CSL — пока есть интернет — или файлом.</p>
      {list.length > 0 && (
        <ul>
          {list.map((s) => (
            <li key={s.id}>{s.title}</li>
          ))}
        </ul>
      )}
      <div className="lookup-line">
        <input aria-label="Имя стиля в репозитории CSL" placeholder="nature" value={name} onChange={(e) => setName(e.target.value)} />
        <button type="button" onClick={byName} disabled={!name.trim()}>
          Добавить
        </button>
        <label className="file-button">
          Файл .csl
          <input type="file" accept=".csl,.xml" className="visually-hidden" onChange={(e) => fromFile(e.target.files?.[0])} />
        </label>
      </div>
      {added && <p role="status">Добавлен стиль «{added}».</p>}
      {problem && (
        <p className="problem" role="alert">
          {problem}
        </p>
      )}
    </section>
  )
}
