FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o treasuryd ./cmd/treasuryd/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/treasuryd /usr/local/bin/treasuryd
EXPOSE 8091
ENTRYPOINT ["treasuryd"]
