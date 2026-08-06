# syntax=docker/dockerfile:1

# ---------- build ----------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies are copied and downloaded before the source so this layer is reused
# whenever only application code changes — the usual case. Editing a handler then
# costs a compile, not a full module re-download.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO_ENABLED=0 produces a statically linked binary, which is what lets the runtime
# stage be a bare alpine with no libc compatibility to worry about. Nothing here needs
# cgo: the pgx driver is pure Go, and the SQL migrations are compiled in via go:embed
# (internal/repository/db.go), so no migrations/ directory has to ship alongside it.
#
# -trimpath strips local filesystem paths from the binary; -s -w drop the symbol and
# DWARF tables. Both shrink the image and keep build-machine paths out of stack traces.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api

# ---------- runtime ----------
FROM alpine:3.21

# ca-certificates is not needed for Postgres over plain TCP, but is needed the moment
# anything talks TLS (an sslmode=require DSN, or any outbound HTTPS call). Cheap insurance.
# wget comes from busybox and is what the compose healthcheck uses.
RUN apk add --no-cache ca-certificates tzdata

# Run unprivileged. A root process in a container is still root on the host if it ever
# escapes the namespace, and this binary needs nothing that root provides.
RUN addgroup -S app && adduser -S -G app app
USER app

WORKDIR /app
COPY --from=build /out/api /app/api

EXPOSE 8080

# No CMD arguments: cmd/api defaults to "main" when given none. Passing "migrate"
# instead runs migrations and exits — that is what the one-shot migrate service in
# docker-compose.yml does with the same image.
ENTRYPOINT ["/app/api"]
