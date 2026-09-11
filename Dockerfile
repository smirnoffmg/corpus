FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/indexer ./cmd/indexer && go build -o /out/mcpd ./cmd/mcpd

FROM alpine:3.22
# poppler-utils supplies pdftotext; there is no Go PDF text extractor of
# comparable quality, so the container carries the system one.
RUN apk add --no-cache poppler-utils ca-certificates
COPY --from=build /out/indexer /out/mcpd /usr/local/bin/
COPY migrations /app/migrations
