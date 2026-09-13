# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/bookmarks ./cmd/bookmarks

FROM alpine:3.21 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/bookmarks /bookmarks
EXPOSE 8080
ENV LISTEN=0.0.0.0:8080 \
    DB_PATH=/data/bookmarks.sqlite \
    BACKUP_DIR=/backups
VOLUME ["/data", "/backups"]
ENTRYPOINT ["/bookmarks"]
