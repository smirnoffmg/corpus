import { highlightSegments } from '../text'

export function Highlight({ text }: { text: string }) {
  return (
    <>
      {highlightSegments(text).map((s, i) => (s.marked ? <mark key={i}>{s.text}</mark> : <span key={i}>{s.text}</span>))}
    </>
  )
}
