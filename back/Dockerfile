FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/indexer ./cmd/indexer && go build -o /out/mcpd ./cmd/mcpd

FROM alpine:3.22
# poppler-utils supplies pdftotext; there is no Go PDF text extractor of
# comparable quality, so the container carries the system one. Tesseract reads
# scanned books, which have no text for pdftotext to pull; only the Russian and
# English models, the languages of this library.
RUN apk add --no-cache poppler-utils ca-certificates \
    tesseract-ocr tesseract-ocr-data-rus tesseract-ocr-data-eng
COPY --from=build /out/indexer /out/mcpd /usr/local/bin/
# Neither binary writes to disk, reads the library and the vault read-only, and
# needs no privileged port. Nothing here wants root.
USER nobody
