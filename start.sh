#!/bin/bash
# 工时记录后端启动脚本
# 用法: ./start.sh

cd "$(dirname "$0")"

export MYSQL_DSN='mosea:9527@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local'

# 微信登录密钥（来自原 main.go）
export WX_APPID=wx3c1dd870d0d09efa
export WX_SECRET=b00c0c5c3ad6e4fe093742f97abb3d5b

# 杀掉旧进程，避免端口占用
pkill -f hour-record-api 2>/dev/null

# 编译（有缓存时很快）
go build -o hour-record-api . || exit 1

echo "starting hour-record-api on :8080 ..."
exec ./hour-record-api
