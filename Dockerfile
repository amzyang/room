# 受限网络环境可用 --build-arg 换用可达的镜像（如内部 registry 代理）
ARG GO_IMAGE=golang:1.26
ARG RUNTIME_IMAGE=alpine:3.24

FROM ${GO_IMAGE} AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /room .

FROM ${RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates \
 && addgroup -S -g 1001 app \
 && adduser -S -u 1001 -G app app

WORKDIR /app

COPY --from=builder /room ./room

RUN mkdir -p .cache/holidays && chown -R app:app /app

USER app

ENTRYPOINT ["/app/room"]
