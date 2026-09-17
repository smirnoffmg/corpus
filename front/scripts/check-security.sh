#!/bin/sh
# Checks, against the running stack, the defences of a server with no login:
# the UI's own security headers, and that the sources served for "open the
# original" cannot reach the API. The corpus has no login, so anything that
# runs on the UI's origin can read the whole library, diary included — and
# manuals are HTML with scripts, uploaded by anyone who has the UI open. They
# are therefore served from an origin of their own, a separate port.
#
#   front/scripts/check-security.sh [UI base] [sources base]
#   (defaults http://localhost:8081 and http://localhost:8082)
set -eu
ui=${1:-http://localhost:8081}
sources=${2:-http://localhost:8082}
library="${LIBRARY_DIR:-$HOME/.corpus/data}"
fail=0
check() { if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; fail=1; fi; }
status() { curl -s -o /dev/null -w %{http_code} "$@" || true; }

page=$(curl -s "$ui/api/sources?kind=docs" | sed -n 's/.*"path": "\([^"]*\.html\)".*/\1/p' | head -1)
[ -n "$page" ] || { echo "no manual page indexed to check against"; exit 1; }
manual=$(echo "$page" | cut -d/ -f1)
book=$(curl -s "$ui/api/sources?kind=book" | sed -n 's/.*"path": "\([^"]*\.pdf\)".*/\1/p' | head -1)
book=$(printf %s "$book" | python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.stdin.read()))')

headers=$(curl -sI "$ui/" | tr -d '\r')
assets=$(curl -sI "$ui/assets/$(curl -s "$ui/" | sed -n 's|.*src="/assets/\([^"]*\.js\)".*|\1|p' | head -1)" | tr -d '\r')
csp=$(echo "$headers" | sed -n 's/^[Cc]ontent-[Ss]ecurity-[Pp]olicy: //p')
check "the UI sends a Content-Security-Policy" '[ -n "$csp" ]'
check "the UI allows scripts from itself only" 'echo "$csp" | grep -q "script-src '"'"'self'"'"'" && ! echo "$csp" | grep -q "script-src[^;]*unsafe"'
check "the UI takes no plugins and no base-URI rewrites" 'echo "$csp" | grep -q "object-src '"'"'none'"'"'" && echo "$csp" | grep -q "base-uri '"'"'none'"'"'"'
check "the UI cannot be framed" 'echo "$csp" | grep -q "frame-ancestors '"'"'none'"'"'"'
check "the UI is not content-sniffed" 'echo "$headers" | grep -qi "^x-content-type-options: nosniff"'
check "the UI sends no referrer to the sources it opens" 'echo "$headers" | grep -qi "^referrer-policy: no-referrer"'
check "cached assets keep the security headers" 'echo "$assets" | grep -qi "^x-content-type-options: nosniff" && echo "$assets" | grep -qi "^content-security-policy:"'
check "Postgres is not published to the host" '! nc -z 127.0.0.1 5433 2>/dev/null'

check "manuals are not served from the UI's origin" '[ "$(status "$ui/docs/$page")" = 404 ]'
check "books are not served from the UI's origin" '[ "$(status "$ui/books/$book")" = 404 ]'
check "publications are not served from the UI's origin" '[ "$(status "$ui/papers/probe.pdf")" = 404 ]'
check "manuals are served from the sources origin" '[ "$(status "$sources/docs/$page")" = 200 ]'
check "the sources origin has no API" '[ "$(status "$sources/api/status")" = 404 ]'
check "the sources origin refuses other host names" '[ "$(status -H "Host: rebind.attacker.test" "$sources/docs/$page")" = 403 ]'

pdf=$(curl -sI "$sources/books/$book" | tr -d '\r')
check "a book is served as a PDF" 'echo "$pdf" | grep -qi "^content-type: application/pdf"'
check "a book is not content-sniffed" 'echo "$pdf" | grep -qi "^x-content-type-options: nosniff"'
for shelf in books papers; do
  [ -d "$library/$shelf" ] || continue
  # A real file, so a 404 means refused rather than absent. Only PDFs are
  # walked, so it never becomes a source.
  echo '<script>fetch("/api/status")</script>' > "$library/$shelf/isolation-probe.html"
  code=$(status "$sources/$shelf/isolation-probe.html")
  rm -f "$library/$shelf/isolation-probe.html"
  check "nothing but PDFs is served from the $shelf ($code)" '[ "$code" = 404 ]'
done

# The attack itself, in a real browser, when there is one: a script on a
# manual page tries to read the API and to write through it.
chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
probe="$library/manuals/$manual/_static/isolation-probe.html"
if [ -x "$chrome" ] && [ -d "$(dirname "$probe")" ]; then
  sed "s|UI_BASE|$ui|g" > "$probe" <<'HTML'
<!doctype html><pre id="out">running</pre><script>
(async () => {
  const r = [];
  for (const [m, u] of [['GET', 'UI_BASE/api/status'], ['POST', 'UI_BASE/api/styles'], ['GET', '/api/status']]) {
    try { const res = await fetch(u, { method: m, body: m === 'POST' ? 'x' : undefined }); r.push(`${m} ${u} ${res.status}`); }
    catch (e) { r.push(`${m} ${u} blocked`); }
  }
  let storage = 'ok';
  try { localStorage.getItem('mode'); } catch (e) { storage = 'throws'; }
  r.push('storage ' + storage);
  document.getElementById('out').textContent = r.join(';');
})();
</script>
HTML
  # _static is not walked by the indexer, so the probe never becomes a source.
  result=$("$chrome" --headless=new --disable-gpu --virtual-time-budget=8000 --dump-dom \
    "$sources/docs/$manual/_static/isolation-probe.html" 2>/dev/null | sed -n 's/.*<pre id="out">\([^<]*\)<.*/\1/p')
  rm -f "$probe"
  echo "     probe: $result"
  check "a manual page's script cannot read the API" 'echo "$result" | grep -q "GET $ui/api/status blocked"'
  check "a manual page's script cannot write through the API" 'echo "$result" | grep -Eq "POST $ui/api/styles (blocked|403)"'
  check "a manual page keeps storage of its own, so themes work" 'echo "$result" | grep -q "storage ok"'
else
  echo "skip browser probe (no Chrome or no manuals directory)"
fi
exit $fail
