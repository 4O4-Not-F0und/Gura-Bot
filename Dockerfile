FROM golang:alpine AS builder

WORKDIR /telegram_translate_bot
COPY . /telegram_translate_bot

ENV CGO_ENABLED 0
ENV GOOS linux
ENV GOARCH amd64

RUN go mod tidy && go test ./... && go build -trimpath -ldflags="-w -s" -o telegram_translate_bot

FROM alpine:latest AS runner

WORKDIR /telegram_translate_bot
COPY --from=builder /telegram_translate_bot/telegram_translate_bot .
COPY --from=builder /telegram_translate_bot/config.example.yml config.yml

EXPOSE 9091/tcp

ENTRYPOINT ["./telegram_translate_bot"]
