# hour-record-api

工时记录小程序的 Go 后端服务。配套前端见 `../miniapp`，项目总览见 `../README.md`。

核心功能：上下班打卡（自动交替/自动结转）、补卡与补下班、按用户标准时间计算工时与加班、周/月/年统计、月历按天汇总、CSV 导出、openid 鉴权。

## 目录结构

```
worktime/
├── miniapp/          # 微信小程序前端（独立仓库）
│   └── pages/index/  # 主页：底部Tab切换 日历视图 / 打卡视图
└── server/           # Go 后端（本目录）
    ├── main.go       # 全部接口 + 数据库初始化
    └── start.sh      # 一键启动脚本
```

## 运行

1. 本机 MySQL（默认 `127.0.0.1:3306`），数据库和表**自动创建**（库名 `hour_record`）。
2. 一键启动（推荐）：

```bash
./start.sh
```

脚本内容：设置 `MYSQL_DSN`、`WX_APPID`/`WX_SECRET`（在脚本里填写）→ 杀旧进程 → 编译 → 启动 `:8080`。

手动启动：

```bash
export MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local'
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

除登录接口外，所有请求需携带 `openid`，后端校验其与 `user_id` 匹配（防止伪造 user_id 操作他人数据），不匹配返回「登录状态无效」。

| 接口 | 方法 | 说明 |
|------|------|------|
| `/ping` | GET | 健康检查 |
| `/api/wx/login` | POST | `{code}`，微信登录，返回 `{openid, user_id}` |
| `/api/user/save` | POST | 保存昵称/头像/标准上下班时间，返回 `user_id` |
| `/api/record/start` | POST | 上班打卡 `{user_id, openid, remark?}`。已有未结束记录时**自动结转下班**再开新记录；跨天悬空记录结转为前一天 23:59:59 |
| `/api/record/end` | POST | 下班打卡 `{user_id, openid}` 结束当前记录；或 `{user_id, openid, id, end_time}` **补下班**（给指定悬空记录补结束时间） |
| `/api/record/add` | POST | 补卡 `{user_id, openid, start_time, end_time, remark?}` |
| `/api/record/delete` | POST | 删除记录 `{user_id, openid, id}` |
| `/api/record/list` | POST | `{user_id, openid, tab, offset?, date?, page, page_size}`，tab: 0日/1周/2月/3年；`offset` 翻历史时段；`date`(YYYY-MM-DD) 查指定日全部打卡；返回分页明细 + 按天汇总 `summary` + 范围文案 `range` |
| `/api/stats` | POST | `{user_id, openid}`，返回当日/周/月/年的天数、总工时、加班 |
| `/api/record/export` | GET | CSV 导出 `?user_id=&openid=` |

时间格式说明（`start_time`/`end_time`）：`HH:MM`（按今天）、`YYYY-MM-DD HH:MM[:SS]`（指定日期，补卡用）、RFC3339。

响应统一为 `{code: 0/-1, msg, data}`。

## 设计说明

- 时间字段统一 **BIGINT 存 Unix 秒**，规避 MySQL 时区问题。
- 打卡自动交替：无未结束记录 → 打卡记为上班；有未结束记录 → 自动结转下班再开新记录。一天可多次打卡。
- 统计口径：每天只取**最早上班 + 最晚下班**计算工时与加班（多次打卡不叠加）。
- 忘打下班兜底：隔天上班打卡时，前一天悬空记录自动结转为当天 23:59:59（当天内结转则用当前时间）。
- 加班 = 结束时间超出「当天标准下班时间」的部分，由服务端按用户设置计算。
- 登录态：小程序 wx.login 换 openid，后续请求自动携带，服务端校验归属。

## 前端使用

1. 微信开发者工具打开 `worktime/miniapp`。
2. 「详情 → 本地设置」勾选**不校验合法域名**（开发阶段用 http）。
3. `pages/index/index.js` 顶部 `baseUrl`：模拟器 `http://127.0.0.1:8080`；真机改电脑局域网 IP；上线改 HTTPS 域名。
4. 没有后端/密钥时，把 `index.js` 里 `MOCK` 改为 `true` 可用模拟数据测试 UI。
