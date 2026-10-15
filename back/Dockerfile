FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/indexer ./cmd/indexer && go build -o /out/mcpd ./cmd/mcpd && go build -o /out/shrink ./cmd/shrink

FROM alpine:3.22
# poppler-utils supplies pdftotext; there is no Go PDF text extractor of
# comparable quality, so the container carries the system one. Tesseract reads
# scanned books, which have no text for pdftotext to pull; only the Russian and
# English models, the languages of this library.
# pikepdf and Pillow re-encode the images of uploaded PDFs (internal/shrink),
# leaving their text and fonts as they are; no Go library decodes the image
# formats a PDF carries as completely.
RUN apk add --no-cache poppler-utils ca-certificates \
    tesseract-ocr tesseract-ocr-data-rus tesseract-ocr-data-eng \
    python3 py3-pikepdf py3-pillow
COPY --from=build /out/indexer /out/mcpd /out/shrink /usr/local/bin/
# No binary here needs a privileged port or a file outside the mounted library.
# Nothing here wants root.
USER nobody
