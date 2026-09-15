// RecognisedBadge marks text read from a scan by OCR: a quote from it may
// carry misread letters, so it is worth checking against the page before it
// goes into a paper.
export function RecognisedBadge() {
  return (
    <span className="recognised" title="Текст распознан со скана: сверьте цитату со страницей оригинала">
      Распознано
    </span>
  )
}
