# syntax=docker/dockerfile:1

FROM golang:1.25 as builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/vaultmail ./main.go

FROM gcr.io/distroless/base-debian12
WORKDIR /
COPY --from=builder /out/vaultmail /vaultmail
EXPOSE 8080
ENTRYPOINT ["/vaultmail"]
