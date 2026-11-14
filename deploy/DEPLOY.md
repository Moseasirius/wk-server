# 部署清单（从零到上线）

前提：服务器已购买（Ubuntu 22.04+）、域名已备案并解析到服务器 IP。

## 0. 本机交叉编译

```bash
cd ~/Code/WeChatDev/worktime/wk-server
GOOS=linux GOARCH=amd64 go build -o hour-record-api .
```

## 1. 服务器装 MySQL

```bash
ssh root@服务器IP
apt update && apt install -y mysql-server caddy
systemctl enable --now mysql
mysql -e "CREATE USER 'hourrec'@'localhost' IDENTIFIED BY '强密码';
CREATE DATABASE hour_record DEFAULT CHARSET utf8mb4;
GRANT ALL ON hour_record.* TO 'hourrec'@'localhost';"
```

## 2. 上传程序

```bash
# 本机执行
scp hour-record-api root@服务器IP:/opt/hour-record/hour-record-api
scp deploy/hour-record.service deploy/env.example root@服务器IP:/tmp/
scp deploy/Caddyfile root@服务器IP:/tmp/
```

## 3. 服务器上配置并启动

```bash
cd /opt/hour-record
cp /tmp/env.example env && vim env          # 填入真实 MySQL 密码和 WX_SECRET
chmod +x hour-record-api
cp /tmp/hour-record.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now hour-record
curl http://127.0.0.1:8080/ping             # 验证: pong
```

## 4. HTTPS（Caddy）

```bash
# 先到云厂商控制台把域名 A 记录指向服务器 IP
vim /tmp/Caddyfile                          # 把域名改成你的
cp /tmp/Caddyfile /etc/caddy/Caddyfile
systemctl reload caddy
# 验证（证书自动申请，需域名解析生效 + 80/443 端口开放）
curl https://你的域名/ping
```

安全组/防火墙需放行：80、443（8080 不用对外暴露，走 Caddy 反代）。

## 5. 小程序后台配置

mp.weixin.qq.com → 开发管理 → 开发设置 → 服务器域名：
- request 合法域名：`https://你的域名`
- downloadFile 合法域名：`https://你的域名`（CSV 导出）

## 6. 发布

1. `wk-miniapp/pages/index/index.js` 的 `baseUrl` 改为 `https://你的域名`
2. 开发者工具「上传」→ 填版本号和备注
3. 后台「版本管理」→ 提交审核（个人主体工具类一般 1~2 天）
4. 过审后发布；或先设为体验版，扫码即用

## 日常运维

```bash
systemctl status hour-record     # 看状态
journalctl -u hour-record -f     # 看日志
systemctl restart hour-record    # 重启
# 更新代码：本机重新编译 → scp 覆盖 → systemctl restart hour-record
```

## 回滚/备份

```bash
# 数据库备份（建议加 crontab 每日一次）
mysqldump hour_record > /root/hour_record_$(date +%F).sql
```
