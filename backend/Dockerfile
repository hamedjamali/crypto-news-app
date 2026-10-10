# مرحله ۱: بیلد
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o crypto-news-backend main.go

# مرحله ۲: اجرا
FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite-libs tzdata

WORKDIR /app

COPY --from=builder /app/crypto-news-backend .

# پوشه دیتابیس
RUN mkdir -p /app/data

EXPOSE 3000

CMD ["./crypto-news-backend"]
