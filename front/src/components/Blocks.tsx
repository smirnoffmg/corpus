import { blocks } from '../text'

export function Blocks({ body }: { body: string }) {
  return (
    <>
      {blocks(body).map((b, i) =>
        b.kind === 'code' ? (
          <pre key={i}>
            <code>{b.content}</code>
          </pre>
        ) : (
          <p key={i}>{b.content}</p>
        ),
      )}
    </>
  )
}
