package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ---------- 通用响应 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]any{"code": -1, "msg": msg})
}

func writeOk(w http.ResponseWriter, msg string) {
	writeJSON(w, map[string]any{"code": 0, "msg": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, "method not allow")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, "参数错误")
		return false
	}
	return true
}

// ---------- 数据库 ----------

var db *sql.DB

var schemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS users (
	id        BIGINT AUTO_INCREMENT PRIMARY KEY,
	openid    VARCHAR(64)  NOT NULL UNIQUE,
	nickname  VARCHAR(128) DEFAULT '',
	avatar    VARCHAR(512) DEFAULT '',
	std_start VARCHAR(8)   DEFAULT '09:00',
	std_end   VARCHAR(8)   DEFAULT '18:00',
	created_at BIGINT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS hour_records (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	user_id    BIGINT NOT NULL,
	start_time BIGINT NOT NULL,
	end_time   BIGINT NULL,
	remark     VARCHAR(512) DEFAULT '',
	created_at BIGINT,
	INDEX idx_records_user (user_id, start_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}

func initDB() error {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root:@tcp(127.0.0.1:3306)/hour_record?charset=utf8mb4&loc=Local"
	}

	// 先不带库名连接，自动建库
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("解析 DSN 失败: %w", err)
	}
	dbName := cfg.DBName
	cfg.DBName = ""
	server, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer server.Close()
	if _, err = server.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4", dbName)); err != nil {
		// 数据库已存在但用户无建库权限时不阻塞，交给后面的连接验证
		fmt.Println("create database warn:", err.Error())
	}

	db, err = sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(10)
	for _, stmt := range schemaStmts {
		if _, err = db.Exec(stmt); err != nil {
			return fmt.Errorf("初始化表失败: %w", err)
		}
	}
	return nil
}

// ---------- 时间工具 ----------

const timeLayout = "2006-01-02 15:04"

// 按 tab(0当日 1周度 2月度 3年度) 计算 [start, end) 时间区间
// offset: 0=当前时段, 1=上一个时段(上周/上月/去年), 2=上两个时段...
func rangeOf(now time.Time, tab int, offset int) (time.Time, time.Time) {
	switch tab {
	case 1: // 周度（周一开始）
		base := now.AddDate(0, 0, -7*offset)
		y, m, d := base.Date()
		wd := int(base.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := time.Date(y, m, d-wd+1, 0, 0, 0, 0, base.Location())
		return monday, monday.AddDate(0, 0, 7)
	case 2: // 月度
		base := now.AddDate(0, -offset, 0)
		y, m, _ := base.Date()
		first := time.Date(y, m, 1, 0, 0, 0, 0, base.Location())
		return first, first.AddDate(0, 1, 0)
	case 3: // 年度
		base := now.AddDate(-offset, 0, 0)
		first := time.Date(base.Year(), 1, 1, 0, 0, 0, 0, base.Location())
		return first, first.AddDate(1, 0, 0)
	default: // 当日
		base := now.AddDate(0, 0, -offset)
		y, m, d := base.Date()
		start := time.Date(y, m, d, 0, 0, 0, 0, base.Location())
		return start, start.AddDate(0, 0, 1)
	}
}

// 加班 = 结束时间超过“开始当天标准下班时间”的部分
func calcHours(startTs int64, endTs int64, stdEnd string) (total float64, overtime float64) {
	total = float64(endTs-startTs) / 3600
	start := time.Unix(startTs, 0)
	y, m, d := start.Date()
	var hh, mm int
	fmt.Sscanf(stdEnd, "%d:%d", &hh, &mm)
	preEnd := time.Date(y, m, d, hh, mm, 0, 0, start.Location())
	if endTs > preEnd.Unix() {
		overtime = float64(endTs-preEnd.Unix()) / 3600
	}
	return
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// ---------- 登录 ----------

func wxLoginHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Code == "" {
		writeErr(w, "code 不能为空")
		return
	}
	appID := os.Getenv("WX_APPID")
	appSecret := os.Getenv("WX_SECRET")
	if appID == "" || appSecret == "" {
		writeErr(w, "服务端未配置 WX_APPID / WX_SECRET 环境变量")
		return
	}
	url := fmt.Sprintf("https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		appID, appSecret, req.Code)
	resp, err := http.Get(url)
	if err != nil {
		writeErr(w, "请求微信接口失败")
		return
	}
	defer resp.Body.Close()

	var wxResult map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&wxResult); err != nil {
		writeErr(w, "解析微信返回失败")
		return
	}
	openid, ok := wxResult["openid"].(string)
	if !ok || openid == "" {
		writeErr(w, "获取openid失败，errmsg:"+fmt.Sprintf("%v", wxResult["errmsg"]))
		return
	}

	// 存在则跳过（INSERT IGNORE），返回 user_id
	res, err := db.Exec("INSERT IGNORE INTO users(openid, created_at) VALUES(?,?)", openid, time.Now().Unix())
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	var userID int64
	if n, _ := res.RowsAffected(); n > 0 {
		userID, _ = res.LastInsertId()
	} else {
		err = db.QueryRow("SELECT id FROM users WHERE openid=?", openid).Scan(&userID)
		if err != nil {
			writeErr(w, "数据库错误: "+err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{
		"code": 0,
		"data": map[string]any{"openid": openid, "user_id": userID},
	})
}

// ---------- 用户信息（昵称 / 标准上下班时间） ----------

func saveUserHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OpenID   string `json:"openid"`
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
		StdStart string `json:"std_start"`
		StdEnd   string `json:"std_end"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.OpenID == "" {
		writeErr(w, "openid 不能为空")
		return
	}
	if req.StdStart == "" {
		req.StdStart = "09:00"
	}
	if req.StdEnd == "" {
		req.StdEnd = "18:00"
	}
	_, err := db.Exec(`
		INSERT INTO users(openid, nickname, avatar, std_start, std_end, created_at)
		VALUES(?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE
			nickname=VALUES(nickname), avatar=VALUES(avatar),
			std_start=VALUES(std_start), std_end=VALUES(std_end)`,
		req.OpenID, req.Nickname, req.Avatar, req.StdStart, req.StdEnd, time.Now().Unix())
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	var userID int64
	db.QueryRow("SELECT id FROM users WHERE openid=?", req.OpenID).Scan(&userID)
	writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"user_id": userID}})
}

// ---------- 打卡 ----------

// 校验 openid 与 user_id 匹配，防止伪造 user_id 操作他人数据
func authOK(userID int64, openid string) bool {
	if openid == "" || userID <= 0 {
		return false
	}
	var dbOpenid string
	if err := db.QueryRow("SELECT openid FROM users WHERE id=?", userID).Scan(&dbOpenid); err != nil {
		return false
	}
	return dbOpenid == openid
}

func hasRunningRecord(userID int64) bool {
	var n int
	db.QueryRow("SELECT COUNT(*) FROM hour_records WHERE user_id=? AND end_time IS NULL", userID).Scan(&n)
	return n > 0
}

func startRecordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64  `json:"user_id"`
		OpenID string `json:"openid"`
		Remark string `json:"remark"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	now := time.Now().Unix()
	if hasRunningRecord(req.UserID) {		// 自动把上一条未结束的打卡结转下班：
		// 同一天 → 记为现在；跨天（忘了打下班）→ 记为前一天 23:59:59
		var startTs int64
		if row := db.QueryRow("SELECT start_time FROM hour_records WHERE user_id=? AND end_time IS NULL LIMIT 1",
			req.UserID); row.Scan(&startTs) == nil {
			endTs := now
			st := time.Unix(startTs, 0)
			nt := time.Unix(now, 0)
			if st.Format("2006-01-02") != nt.Format("2006-01-02") {
				endTs = time.Date(st.Year(), st.Month(), st.Day(), 23, 59, 59, 0, st.Location()).Unix()
			}
			if _, err := db.Exec("UPDATE hour_records SET end_time=? WHERE user_id=? AND end_time IS NULL",
				endTs, req.UserID); err != nil {
				writeErr(w, "数据库错误: "+err.Error())
				return
			}
		}
	}
	_, err := db.Exec("INSERT INTO hour_records(user_id, start_time, remark, created_at) VALUES(?,?,?,?)",
		req.UserID, now, req.Remark, now)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	writeOk(w, "打卡成功")
}

func endRecordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int64  `json:"user_id"`
		OpenID  string `json:"openid"`
		ID      int64  `json:"id"`       // 补下班：指定要结转的记录
		EndTime string `json:"end_time"` // 补下班：指定结束时间
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	// 补下班：给指定的未结束记录补一个结束时间
	if req.ID > 0 {
		if req.EndTime == "" {
			writeErr(w, "end_time 为空")
			return
		}
		endTs, err := parseTimeInput(req.EndTime, time.Local)
		if err != nil {
			writeErr(w, "end_time "+err.Error())
			return
		}
		res, err := db.Exec(
			"UPDATE hour_records SET end_time=? WHERE id=? AND user_id=? AND end_time IS NULL",
			endTs, req.ID, req.UserID)
		if err != nil {
			writeErr(w, "数据库错误: "+err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeErr(w, "记录不存在或已结束")
			return
		}
		writeOk(w, "补下班成功")
		return
	}
	now := time.Now().Unix()
	res, err := db.Exec(
		"UPDATE hour_records SET end_time=? WHERE user_id=? AND end_time IS NULL",
		now, req.UserID)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, "没有正在打卡的记录")
		return
	}
	writeOk(w, "结束打卡成功")
}

// 手动补卡：start_time / end_time 支持 "HH:MM"(今天) 或 RFC3339
func parseTimeInput(s string, loc *time.Location) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("时间为空")
	}
	if t, err := time.ParseInLocation("15:04", s, loc); err == nil {
		now := time.Now().In(loc)
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc).Unix(), nil
	}
	// 支持指定日期时间（补卡用）
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.Unix(), nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("时间格式无效: %s", s)
}

func addRecordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID    int64  `json:"user_id"`
		OpenID    string `json:"openid"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Remark    string `json:"remark"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	startTs, err := parseTimeInput(req.StartTime, time.Local)
	if err != nil {
		writeErr(w, "start_time "+err.Error())
		return
	}
	endTs := time.Now().Unix()
	if req.EndTime != "" {
		if endTs, err = parseTimeInput(req.EndTime, time.Local); err != nil {
			writeErr(w, "end_time "+err.Error())
			return
		}
	}
	if endTs <= startTs {
		writeErr(w, "结束时间必须晚于开始时间")
		return
	}
	now := time.Now().Unix()
	_, err = db.Exec("INSERT INTO hour_records(user_id, start_time, end_time, remark, created_at) VALUES(?,?,?,?,?)",
		req.UserID, startTs, endTs, req.Remark, now)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	writeOk(w, "添加成功")
}

func deleteRecordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64  `json:"user_id"`
		OpenID string `json:"openid"`
		ID     int64  `json:"id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	res, err := db.Exec("DELETE FROM hour_records WHERE id=? AND user_id=?", req.ID, req.UserID)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, "记录不存在")
		return
	}
	writeOk(w, "删除成功")
}

// ---------- 记录列表 ----------

type recordRow struct {
	ID       int64
	StartTs  int64
	EndTs    sql.NullInt64
	Remark   string
	StdStart string
	StdEnd   string
}

func listRecordHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   int64  `json:"user_id"`
		OpenID   string `json:"openid"`
		Tab      int    `json:"tab"`    // 0考勤 1周度 2月度 3年度
		Offset   int    `json:"offset"` // 0当前时段，1上一个时段...
		Date     string `json:"date"`   // 考勤视图：指定日期(YYYY-MM-DD)查当天明细
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}
	from, to := rangeOf(time.Now(), req.Tab, req.Offset)
	// 考勤视图：按指定日期查当天明细
	if req.Tab == 0 && req.Date != "" {
		if t, perr := time.ParseInLocation("2006-01-02", req.Date, time.Local); perr == nil {
			from = t
			to = t.AddDate(0, 0, 1)
		}
	}

	rows, err := db.Query(`
		SELECT rec.id, rec.start_time, rec.end_time, rec.remark, u.std_start, u.std_end
		FROM hour_records rec JOIN users u ON u.id = rec.user_id
		WHERE rec.user_id = ? AND rec.start_time >= ? AND rec.start_time < ?
		ORDER BY rec.start_time DESC LIMIT ? OFFSET ?`,
		req.UserID, from.Unix(), to.Unix(), req.PageSize, (req.Page-1)*req.PageSize)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	defer rows.Close()

	type RowItem struct {
		ID        int64  `json:"id"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Duration  string `json:"duration"`
		Overtime  string `json:"overtime"`
		Remark    string `json:"remark"`
	}
	list := []RowItem{}
	for rows.Next() {
		var rec recordRow
		if err := rows.Scan(&rec.ID, &rec.StartTs, &rec.EndTs, &rec.Remark, &rec.StdStart, &rec.StdEnd); err != nil {
			continue
		}
		item := RowItem{
			ID:        rec.ID,
			StartTime: time.Unix(rec.StartTs, 0).Format(timeLayout),
			EndTime:   "",
			Duration:  "--",
			Overtime:  "--",
			Remark:    rec.Remark,
		}
		if rec.EndTs.Valid {
			item.EndTime = time.Unix(rec.EndTs.Int64, 0).Format(timeLayout)
			total, ot := calcHours(rec.StartTs, rec.EndTs.Int64, rec.StdEnd)
			item.Duration = fmt.Sprintf("%.1f 小时", round1(total))
			item.Overtime = fmt.Sprintf("%.1f 小时", round1(ot))
		}
		list = append(list, item)
	}
	rows.Close()

	var total int
	db.QueryRow(
		"SELECT COUNT(*) FROM hour_records WHERE user_id=? AND start_time>=? AND start_time<?",
		req.UserID, from.Unix(), to.Unix()).Scan(&total)

	// 日历/汇总数据：统计已完结的打卡（年度按月汇总）
	type DaySummary struct {
		Date     string  `json:"date"`
		Weekday  string  `json:"weekday"`
		Day      int     `json:"day"`
		Month    int     `json:"month"`
		Hours    float64 `json:"hours"`
		Overtime float64 `json:"overtime"`
		Count    int     `json:"count"`
	}
	weekdayNames := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

	// 按天聚合：每天只取最早的上班和最晚的下班（防止多次打卡重复计时）
	type dayPunch struct {
		first, last int64
		stdEnd      string
		count       int
	}
	punchAgg := map[string]*dayPunch{}
	sumRows, err := db.Query(`
		SELECT rec.start_time, rec.end_time, u.std_end
		FROM hour_records rec JOIN users u ON u.id = rec.user_id
		WHERE rec.user_id=? AND rec.end_time IS NOT NULL AND rec.start_time>=? AND rec.start_time<?
		ORDER BY rec.start_time ASC`,
		req.UserID, from.Unix(), to.Unix())
	if err == nil {
		for sumRows.Next() {
			var s, e int64
			var stdEnd string
			if err := sumRows.Scan(&s, &e, &stdEnd); err != nil {
				continue
			}
			date := time.Unix(s, 0).Format("2006-01-02")
			day, ok := punchAgg[date]
			if !ok {
				day = &dayPunch{stdEnd: stdEnd}
				punchAgg[date] = day
			}
			if day.first == 0 || s < day.first {
				day.first = s
			}
			if e > day.last {
				day.last = e
			}
			day.count++
		}
		sumRows.Close()
	}

	// 某天的工时/加班：最早上班 → 最晚下班
	dayStats := func(date string) (float64, float64, int) {
		da, ok := punchAgg[date]
		if !ok || da.first == 0 || da.last == 0 {
			return 0, 0, 0
		}
		h, ot := calcHours(da.first, da.last, da.stdEnd)
		return round1(h), round1(ot), da.count
	}

	// 覆盖整个时段：没有记录的天/月也给零值，方便前端画日历
	var sumList []DaySummary
	if req.Tab == 3 {
		// 年度：12 个月各一条
		sumList = []DaySummary{}
		for mm := 1; mm <= 12; mm++ {
			entry := DaySummary{Month: mm, Date: fmt.Sprintf("%04d-%02d", from.Year(), mm)}
			for date, da := range punchAgg {
				if date[:7] != entry.Date || da.first == 0 || da.last == 0 {
					continue
				}
				h, ot := calcHours(da.first, da.last, da.stdEnd)
				entry.Hours = round1(entry.Hours + round1(h))
				entry.Overtime = round1(entry.Overtime + round1(ot))
				entry.Count += da.count
			}
			sumList = append(sumList, entry)
		}
	} else {
		// 当日/周度/月度：每天一条
		sumList = []DaySummary{}
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			date := d.Format("2006-01-02")
			entry := DaySummary{
				Date:    date,
				Weekday: weekdayNames[int(d.Weekday())],
				Day:     d.Day(),
				Month:   int(d.Month()),
			}
			entry.Hours, entry.Overtime, entry.Count = dayStats(date)
			sumList = append(sumList, entry)
		}
	}

	// 月历第一行前面的空白格数（周一为第一列）
	leadingBlanks := 0
	if req.Tab == 2 {
		first := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location())
		leadingBlanks = (int(first.Weekday()) + 6) % 7
	}

	// 当前筛选范围文案
	rangeText := from.Format("2006-01-02")
	switch req.Tab {
	case 1:
		rangeText = from.Format("2006-01-02") + " ~ " + to.AddDate(0, 0, -1).Format("2006-01-02")
	case 2:
		rangeText = from.Format("2006年01月")
	case 3:
		rangeText = from.Format("2006年") + " 全年"
	}

	writeJSON(w, map[string]any{
		"code": 0,
		"data": map[string]any{
			"list":      list,
			"total":     total,
			"page":      req.Page,
			"page_size": req.PageSize,
			"range":     rangeText,
			"summary":   sumList,
			"blanks":    leadingBlanks,
			"today":     time.Now().Format("2006-01-02"),
		},
	})
}

// ---------- 统计 ----------

func statsHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64  `json:"user_id"`
		OpenID string `json:"openid"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !authOK(req.UserID, req.OpenID) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	// 读取用户标准下班时间
	stdEnd := "18:00"
	db.QueryRow("SELECT std_end FROM users WHERE id=?", req.UserID).Scan(&stdEnd)

	now := time.Now()
	stats := map[string]any{}
	ranges := []struct {
		tab int
		key string
	}{{0, "day"}, {1, "week"}, {2, "month"}, {3, "year"}}
	for _, rg := range ranges {
		from, to := rangeOf(now, rg.tab, 0)
		rows, err := db.Query(
			"SELECT start_time, end_time FROM hour_records WHERE user_id=? AND end_time IS NOT NULL AND start_time>=? AND start_time<? ORDER BY start_time ASC",
			req.UserID, from.Unix(), to.Unix())
		if err != nil {
			continue
		}
		// 按天聚合：每天取最早上班和最晚下班
		type dayPunch struct {
			first, last int64
		}
		dayAgg := map[string]*dayPunch{}
		for rows.Next() {
			var s, e int64
			rows.Scan(&s, &e)
			date := time.Unix(s, 0).Format("2006-01-02")
			day, ok := dayAgg[date]
			if !ok {
				day = &dayPunch{}
				dayAgg[date] = day
			}
			if day.first == 0 || s < day.first {
				day.first = s
			}
			if e > day.last {
				day.last = e
			}
		}
		rows.Close()
		var total, overtime float64
		days := 0
		for _, day := range dayAgg {
			t, ot := calcHours(day.first, day.last, stdEnd)
			total += t
			overtime += ot
			days++
		}
		stats[rg.key] = map[string]any{
			"days":     days,
			"hours":    round1(total),
			"overtime": round1(overtime),
		}
	}
	writeJSON(w, map[string]any{"code": 0, "data": stats})
}

// ---------- CSV 导出 ----------

func exportHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	openid := r.URL.Query().Get("openid")
	if !authOK(userID, openid) {
		writeErr(w, "登录状态无效，请重新登录")
		return
	}
	stdEnd := "18:00"
	db.QueryRow("SELECT std_end FROM users WHERE id=?", userID).Scan(&stdEnd)

	rows, err := db.Query(
		"SELECT id, start_time, end_time, remark FROM hour_records WHERE user_id=? ORDER BY start_time ASC", userID)
	if err != nil {
		writeErr(w, "数据库错误: "+err.Error())
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv;charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=worktime.csv")
	cw := csv.NewWriter(w)
	cw.Write([]string{"记录ID", "开始时间", "结束时间", "总时长(小时)", "加班时长(小时)", "备注"})
	for rows.Next() {
		var id, s int64
		var e sql.NullInt64
		var remark string
		rows.Scan(&id, &s, &e, &remark)
		rec := []string{
			strconv.FormatInt(id, 10),
			time.Unix(s, 0).Format(timeLayout),
			"", "", "", remark,
		}
		if e.Valid {
			rec[2] = time.Unix(e.Int64, 0).Format(timeLayout)
			total, ot := calcHours(s, e.Int64, stdEnd)
			rec[3] = fmt.Sprintf("%.1f", round1(total))
			rec[4] = fmt.Sprintf("%.1f", round1(ot))
		}
		cw.Write(rec)
	}
	cw.Flush()
}

// ---------- main ----------

func pingHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"code": 0, "msg": "pong, service ready"})
}

func main() {
	if err := initDB(); err != nil {
		fmt.Println("init db err:", err.Error())
		return
	}
	defer db.Close()

	http.HandleFunc("/ping", pingHandler)
	http.HandleFunc("/api/wx/login", wxLoginHandler)
	http.HandleFunc("/api/user/save", saveUserHandler)
	http.HandleFunc("/api/record/start", startRecordHandler)
	http.HandleFunc("/api/record/end", endRecordHandler)
	http.HandleFunc("/api/record/add", addRecordHandler)
	http.HandleFunc("/api/record/delete", deleteRecordHandler)
	http.HandleFunc("/api/record/list", listRecordHandler)
	http.HandleFunc("/api/stats", statsHandler)
	http.HandleFunc("/api/record/export", exportHandler)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	fmt.Println("server running on", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Println("listen err:", err.Error())
	}
}
