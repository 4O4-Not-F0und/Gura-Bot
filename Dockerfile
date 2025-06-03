FROM golang:alpine AS builder

WORKDIR /gura_bot
COPY . /gura_bot

ENV CGO_ENABLED 0
ENV GOOS linux
ENV GOARCH amd64

RUN go mod tidy && go test ./... && go build -trimpath -ldflags="-w -s" -o gura_bot

FROM alpine:latest AS runner

WORKDIR /gura_bot
COPY --from=builder /gura_bot/gura_bot .
COPY --from=builder /gura_bot/config.example.yml config.yml
COPY --from=builder --chmod=0755 /gura_bot/scripts/reload.sh /usr/sbin/reload.sh

EXPOSE 9091/tcp

ENTRYPOINT ["./gura_bot"]
