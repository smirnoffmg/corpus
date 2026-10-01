import { useEffect, useState } from 'react'
import { sources, status as fetchStatus, type Kind, type SourceStatus, type Status } from '../api'
import { kindFilters } from '../kinds'
import { progress, totalsByKind, withoutVector, type KindTotal } from '../library'
import { plural } from '../text'

const kindLabel = Object.fromEntries(kindFilters.map((f) => [f.value, f.label])) as Record<Kind, string>

const answers = (ok: boolean) => (ok ? 'Работает' : 'Не отвечает')

const rerankerLabel: Record<NonNullable<Status['reranker']>, string> = { ok: 'Работает', unreachable: 'Не отвечает', off: 'Выключен' }

function services(s: Status): [string, string][] {
  const model = s.settings?.model
  const rows: [string, string][] = [
    ['База данных', s.db === 'ok' ? 'Работает' : `Ошибка: ${s.db}`],
    ['Эмбеддер', s.embedder === 'ok' && model ? `Работает · ${model}` : answers(s.embedder === 'ok')],
  ]
  if (s.reranker) rows.push(['Реранкер', rerankerLabel[s.reranker]])
  if (s.uploads !== undefined) rows.push(['Загрузка файлов', s.uploads ? 'Включена' : 'Выключена'])
  return rows
}

function notes(t: KindTotal): string[] {
  const out: string[] = []
  if (t.quarantined) out.push(`${t.quarantined} ${plural(t.quarantined, withoutVector)}`)
  const waiting = t.scans - t.scansFailed
  if (waiting) out.push(`${waiting} ${plural(waiting, ['скан ждёт распознавания', 'скана ждут распознавания', 'сканов ждут распознавания'])}`)
  if (t.scansFailed) out.push(`${t.scansFailed} ${plural(t.scansFailed, ['скан не распознан', 'скана не распознаны', 'сканов не распознаны'])}`)
  return out
}

export function SystemPage({ pollMs = 5000 }: { pollMs?: number }) {
  const [snapshot, setSnapshot] = useState<{ status: Status; list: SourceStatus[] }>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const request = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    const tick = () => {
      Promise.all([fetchStatus(request.signal), sources({}, request.signal)])
        .then(([status, list]) => {
          setSnapshot({ status, list })
          setFailed(false)
        })
        .catch(() => {
          if (!request.signal.aborted) setFailed(true)
        })
        .finally(() => {
          if (!request.signal.aborted) timer = setTimeout(tick, pollMs)
        })
    }
    tick()
    return () => {
      request.abort()
      clearTimeout(timer)
    }
  }, [pollMs])

  const status = snapshot?.status
  const settings = Object.entries(status?.settings ?? {}).sort(([a], [b]) => a.localeCompare(b))

  return (
    <div className="system">
      <h1>Система</h1>
      {failed && (
        <p className="problem" role="alert">
          Сервер не отвечает{snapshot ? ': ниже последнее, что он сообщил.' : '.'}
        </p>
      )}
      {!snapshot && !failed && <p className="quiet">Спрашиваю сервер…</p>}

      {status && (
        <>
          <h2 id="system-services">Службы</h2>
          <table aria-labelledby="system-services">
            <tbody>
              {services(status).map(([name, state]) => (
                <tr key={name}>
                  <th scope="row">{name}</th>
                  <td>{state}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!!status.searches_without_vectors && (
            <p className="quiet">
              С запуска сервера {status.searches_without_vectors}{' '}
              {plural(status.searches_without_vectors, ['поиск прошёл', 'поиска прошли', 'поисков прошли'])} без векторов, по одним словам.
            </p>
          )}

          <h2 id="system-indexing">Индексация</h2>
          <div className="table-scroll">
            <table aria-labelledby="system-indexing">
              <thead>
                <tr>
                  <th scope="col">Тип</th>
                  <th scope="col">Источников</th>
                  <th scope="col">Фрагменты с вектором</th>
                  <th scope="col">Замечания</th>
                </tr>
              </thead>
              <tbody>
                {totalsByKind(snapshot.list).map((t) => {
                  const { state, fraction } = progress(t)
                  return (
                    <tr key={t.kind} className={`state-${state}`}>
                      <th scope="row">{kindLabel[t.kind]}</th>
                      <td>{t.sources}</td>
                      <td>
                        {t.chunks ? (
                          <>
                            <span className="meter" style={{ '--fill': fraction } as React.CSSProperties} aria-hidden="true" />
                            {t.embedded} из {t.chunks}
                          </>
                        ) : (
                          <span className="quiet">Пусто</span>
                        )}
                      </td>
                      <td>{notes(t).join(', ')}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>

          {settings.length > 0 && (
            <>
              <h2 id="system-settings">Настройки</h2>
              <p className="quiet">Задаются флагами при запуске сервера; здесь только для просмотра.</p>
              <table aria-labelledby="system-settings">
                <tbody>
                  {settings.map(([name, value]) => (
                    <tr key={name}>
                      <th scope="row">
                        <code>{name}</code>
                      </th>
                      <td className="detail">{value || <span className="quiet">не задано</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </>
      )}
    </div>
  )
}
