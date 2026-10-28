# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# DB_TAGS selects the database drivers and is additive: empty = SQLite + PostgreSQL,
# "sqlite" = SQLite only, "postgres" = PostgreSQL only. The goolm tag uses the pure Go Olm
# implementation, so cgo is not needed.
ARG DB_TAGS=""
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "goolm ${DB_TAGS}" -o /out/matrixrss ./cmd/matrixrss

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app \
    && mkdir /data && chown app /data

COPY --from=build /out/matrixrss /usr/local/bin/matrixrss

USER app
WORKDIR /data
VOLUME /data
ENV DATABASE_DSN=/data/matrixrss.db \
    HTTP_ADDR=:8080
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=20s \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["matrixrss"]
