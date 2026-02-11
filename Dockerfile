# syntax=docker/dockerfile:1

FROM golang:1.25 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -e; \
  GOOS="${TARGETOS}"; \
  GOARCH="${TARGETARCH}"; \
  GOARM=""; \
  if [ "${TARGETARCH}" = "arm" ]; then \
    case "${TARGETVARIANT}" in \
      v7) GOARM=7 ;; \
      v6) GOARM=6 ;; \
      v5) GOARM=5 ;; \
      "") GOARM=7 ;; \
      *) GOARM="${TARGETVARIANT#v}" ;; \
    esac; \
  fi; \
  env CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" ${GOARM:+GOARM="${GOARM}"} \
    go build -o /out/vaultmail ./main.go

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends bash ca-certificates \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=builder /out/vaultmail /vaultmail
EXPOSE 8080
ENTRYPOINT ["/vaultmail"]
