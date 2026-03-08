# 构建阶段
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o codeflicke2api .

# 运行阶段
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/codeflicke2api .
RUN mkdir -p /app/data

EXPOSE 8080

ENV PORT=8080 \
    ADMIN_TOKEN=123456 \
    DEFAULT_API_KEY=sk-123456 \
    CODEFLICKER_BASE_URL=https://www.codeflicker.ai \
    DB_PATH=/app/data/codeflicke2api.db

VOLUME ["/app/data"]

ENTRYPOINT ["./codeflicke2api"]
