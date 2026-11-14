# hour-record-api

工时记录小程序项目：微信小程序前端（`../SOWT`）+ Go 后端服务（本目录）。

核心功能：上下班打卡、按用户设置的标准时间自动计算工时与加班、日/周/月/年统计（周条/月历/年月卡片视图）、CSV 导出。

## 目录结构

```
WeChatDev/
├── SOWT/                    # 微信小程序前端
│   └── pages/index/         # 主页面（打卡 + 统计 + 明细）
│       ├── index.wxml       # 顶部打卡卡 / Tab / 概览 / 日历视图 / 明细
│       ├── index.js         # 实时时钟、打卡、分页、统计、导出
│       └── index.wxss
└── gowork/hour-record-api/  # Go 后端（本目录）
    ├── main.go
    └── start.sh             # 一键启动脚本
```

## 后端运行

1. 本机 MySQL（默认 `127.0.0.1:3306`），数据库和表**自动创建**（库名 `hour_record`）。
2. 一键启动（推荐）：

```bash
./start.sh
```

脚本内容：设置 `MYSQL_DSN`、`WX_APPID`/`WX_SECRET`（在脚本里填写）→ 杀旧进程 → 编译 → 启动 `:8080`。

手动启动：

```bash
export MYSQL_DSN='mosea:***@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local'
export WX_APPID=你的appid
export WX_SECRET=你的secret
go run main.go
```

> ⚠️ 密钥不要提交到 Git；旧代码曾明文提交过 AppSecret，建议在微信公众平台重置。

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `MYSQL_DSN` | MySQL 连接串 | `root:@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local` |
| `WX_APPID` | 微信小程序 AppID | 无（未配置时登录接口报错） |
| `WX_SECRET` | 微信小程序 AppSecret | 无 |
| `ADDR` | 监听地址 | `0.0.0.0:8080` |

## 接口一览

| 接口 | 方法 | 说明 |
|------|------|------|
| `/ping` | GET | 健康检查 |
| `/api/wx/login` | POST | 微信登录，返回 `{openid, user_id}` |
| `/api/user/save` | POST | 保存昵称/头像/标准上下班时间，返回 `user_id` |
| `/api/record/start` | POST | 上班打卡 `{user_id, remark}` |
| `/api/record/end` | POST | 下班打卡 `{user_id}` |
| `/api/record/add` | POST | 手动补卡 `{user_id, start_time, end_time, remark}`，时间支持 `HH:MM` 或 RFC3339 |
| `/api/record/delete` | POST | 删除记录 `{user_id, id}` |
| `/api/record/list` | POST | `{user_id, tab, offset, page, page_size}`，tab: 0日/1周/2月/3年；`offset` 翻历史时段；返回分页明细 + 全时段按天（年按月）汇总 `summary` + 范围文案 `range` |
| `/api/stats` | POST | `{user_id}`，返回当日/周/月/年的天数、总工时、加班 |
| `/api/record/export` | GET | CSV 导出 `?user_id=1` |

响应统一为 `{code: 0/-1, msg, data}`。

## 设计说明

- 时间字段统一 **BIGINT 存 Unix 秒**，规避 MySQL 时区问题。
- 总时长 = 下班 − 上班；加班时长 = 结束时间超出「当天标准下班时间」的部分，由**服务端**按用户设置计算。
- 打卡防重：有未结束记录时不能再上班打卡。
- 前端页面结构（参考钉钉风格）：深色打卡卡（实时时钟 + 圆形打卡按钮 + 今日工时/加班）→ 日/周/月/年 Tab → 时段概览（总工时/加班/出勤）→ 周条/月历/年月卡片 → 单行打卡明细。

## 前端使用

1. 微信开发者工具打开 `WeChatDev/SOWT`。
2. 「详情 → 本地设置」勾选**不校验合法域名**（开发阶段用 http）。
3. 真机调试时把 `pages/index/index.js` 里的 `baseUrl` 改成电脑局域网 IP。
4. 没有后端/密钥时，把 `index.js` 里 `MOCK` 改为 `true` 可用模拟数据测试 UI。

## 测试数据

`user_id=2`（测试账号）预置了 12 个工作日打卡数据（标准时间 9:00–17:00，含加班），可用 `/api/record/delete` 或页面上的删除按钮清理：

```bash
mysql -umosea -p*** hour_record -e "DELETE FROM hour_records WHERE user_id=2 AND remark IN ('正常','加班');"
```
