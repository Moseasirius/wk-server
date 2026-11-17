# 微信云托管部署用：两阶段构建，最终镜像 ~20MB
FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o wk-server .

FROM alpine:3.19
WORKDIR /app
COPY --from=build /app/wk-server .
EXPOSE 8080
CMD ["./wk-server"]
