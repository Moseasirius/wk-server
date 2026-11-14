#!/bin/bash
# 工时记录后端启动脚本
# 用法: ./start.sh

cd "$(dirname "$0")"

export MYSQL_DSN='mosea:9527@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local'

# 微信登录密钥
export WX_APPID=wx3c1dd870d0d09efa
export WX_SECRET=b00c0c5c3ad6e4fe093742f97abb3d5b

# 杀掉旧进程，避免端口占用
pkill -f wk-server 2>/dev/null

# 编译（有缓存时很快）
go build -o wk-server . || exit 1

echo "starting wk-server on :8080 ..."
exec ./wk-server
