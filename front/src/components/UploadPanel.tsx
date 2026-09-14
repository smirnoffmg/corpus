import { useEffect, useState, type DragEvent } from 'react'
import { ApiError, sources, upload } from '../api'
import { manualName, progress, stateLabel } from '../library'

interface Recent {
  path: string
  label: string
  prefix: string
  counts?: { chunks: number; embedded: number; quarantined: number }
  settled: boolean
}

const accepted = /\.(pdf|zip)$/i

function refusal(e: unknown): string {
  if (!(e instanceof ApiError)) return `Загрузка не удалась: ${String(e)}`
  switch (e.status) {
    case 0:
      return 'Нет связи с сервером. Проверьте, что mcpd запущен.'
    case 409:
      return `Такой файл уже есть в библиотеке: ${e.message}`
    case 413:
      return `Файл слишком большой для загрузки: ${e.message}`
    case 400:
      return `Файл не принят: ${e.message}`
    case 503:
      return `Загрузка выключена на сервере: ${e.message}`
    default:
      return `Загрузка не удалась: ${e.message}`
  }
}

// UploadPanel sends a book or a manual and then follows it through indexing.
// A file that is uploaded but not yet indexed has no row anywhere, so the
// panel keeps its own list of what it sent.
export function UploadPanel({ pollMs, onSettled }: { pollMs: number; onSettled: () => void }) {
  const [file, setFile] = useState<File | null>(null)
  const [manual, setManual] = useState('')
  const [problem, setProblem] = useState<string | null>(null)
  const [sent, setSent] = useState<number | null>(null)
  const [over, setOver] = useState(false)
  const [recent, setRecent] = useState<Recent[]>([])

  const choose = (f: File | undefined) => {
    if (!f) return
    setFile(f)
    setManual(manualName(f.name))
    setProblem(accepted.test(f.name) ? null : `«${f.name}» не подойдёт: загружаются .pdf (книга) и .zip со сборкой HTML-мануала.`)
  }

  const drop = (e: DragEvent) => {
    e.preventDefault()
    setOver(false)
    choose(e.dataTransfer.files[0])
  }

  const isManual = file ? /\.zip$/i.test(file.name) : false

  const send = async () => {
    if (!file) return
    setSent(0)
    setProblem(null)
    try {
      const target = isManual ? { kind: 'docs' as const, manual } : { kind: 'book' as const }
      const done = await upload(file, target, setSent)
      const prefix = done.kind === 'docs' ? `${done.path}/` : done.path
      setRecent((r) => [{ path: done.path, label: file.name, prefix, settled: false }, ...r])
      setFile(null)
    } catch (e) {
      setProblem(refusal(e))
    } finally {
      setSent(null)
    }
  }

  useEffect(() => {
    if (!recent.some((r) => !r.settled)) return
    const timer = setTimeout(async () => {
      const next = await Promise.all(
        recent.map(async (r) => {
          if (r.settled) return r
          try {
            const rows = await sources({ prefix: r.prefix })
            const counts = rows.reduce(
              (sum, s) => ({ chunks: sum.chunks + s.chunks, embedded: sum.embedded + s.embedded, quarantined: sum.quarantined + s.quarantined }),
              { chunks: 0, embedded: 0, quarantined: 0 },
            )
            const { state } = progress(counts)
            return { ...r, counts, settled: state === 'ready' || state === 'errors' }
          } catch {
            return r
          }
        }),
      )
      setRecent(next)
      if (next.some((r, i) => r.settled && !recent[i].settled)) onSettled()
    }, pollMs)
    return () => clearTimeout(timer)
  }, [recent, pollMs, onSettled])

  return (
    <section className="upload" aria-labelledby="upload-title">
      <h2 id="upload-title">Добавить в библиотеку</h2>
      <div
        className={over ? 'dropzone over' : 'dropzone'}
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={drop}
      >
        <p>Перетащите сюда PDF книги или ZIP со сборкой мануала.</p>
        <label className="file-button">
          Выбрать файл
          <input type="file" accept=".pdf,.zip" className="visually-hidden" onChange={(e) => choose(e.target.files?.[0])} />
        </label>
        {file && <p className="chosen">{file.name}</p>}
      </div>

      {file && isManual && !problem && (
        <div className="field">
          <label htmlFor="manual-name">Название мануала</label>
          <input id="manual-name" type="text" value={manual} onChange={(e) => setManual(e.target.value)} aria-describedby="manual-hint" />
          <small id="manual-hint">Так мануал будет называться в цитатах: «{manual || '…'} · страница».</small>
        </div>
      )}

      {problem && (
        <p className="problem" role="alert">
          {problem}
        </p>
      )}

      <div className="upload-actions">
        <button type="button" onClick={send} disabled={!file || !!problem || sent !== null || (isManual && !manual)}>
          Загрузить
        </button>
        {sent !== null && (
          <span className="sending">
            <progress value={sent} max={1} /> Загружаю… {Math.round(sent * 100)}%
          </span>
        )}
      </div>

      {recent.length > 0 && (
        <section aria-label="Загрузки" className="recent">
          <ul>
            {recent.map((r) => (
              <li key={r.path} className={`state-${progress(r.counts ?? { chunks: 0, embedded: 0, quarantined: 0 }).state}`}>
                <span>{r.label}</span>
                <span>{stateLabel(r.counts ?? { chunks: 0, embedded: 0, quarantined: 0 })}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </section>
  )
}
