# syntax=docker/dockerfile:1

FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/vaultmail ./main.go

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends bash ca-certificates \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=builder /out/vaultmail /vaultmail
EXPOSE 8080
ENTRYPOINT ["/vaultmail"]
