package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ==================== Session Manager ====================

type adminSession struct {
	Username string
	Expiry   time.Time
}

var adminSessions = map[string]*adminSession{}

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func getAdminSession(r *http.Request) bool {
	cookie, err := r.Cookie("admin_session")
	if err != nil {
		log.Printf("[DEBUG] getAdminSession: no cookie, err=%v", err)
		return false
	}
	s, ok := adminSessions[cookie.Value]
	log.Printf("[DEBUG] getAdminSession: cookie=%s, found=%v, sessions=%v", cookie.Value[:8]+"...", ok, len(adminSessions))
	if !ok {
		return false
	}
	if time.Now().After(s.Expiry) {
		delete(adminSessions, cookie.Value)
		return false
	}
	return true
}

// ==================== Admin Handlers ====================

// /admin/login
func handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		tmpl, _ := template.ParseFiles("templates/admin_login.html")
		tmpl.Execute(w, nil)
		return
	}
	r.ParseMultipartForm(32 << 20)
	username := strings.TrimSpace(r.FormValue("username"))
	password := strings.TrimSpace(r.FormValue("password"))

	log.Printf("[DEBUG] login: username=%s cfg.AdminUser=%s cfg.AdminPassword=%s",
		username, cfg.AdminUser, cfg.AdminPassword)

	if username != cfg.AdminUser || password != cfg.AdminPassword {
		jsonResp(w, 401, "用户名或密码错误", nil)
		return
	}

	token := generateToken()
	adminSessions[token] = &adminSession{Username: username, Expiry: time.Now().Add(24 * time.Hour)}

	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400,
	})
	jsonResp(w, 200, "登录成功", nil)
}

// /admin/logout
func handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("admin_session"); err == nil {
		delete(adminSessions, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "admin_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// /admin/ — 管理后台主页
func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.ParseFiles("templates/admin.html")
	if err != nil {
		showError(w, "模板加载失败: "+err.Error())
		return
	}
	tmpl.Execute(w, map[string]interface{}{"SiteName": getSiteName()})
}

// /admin/stats — 统计数据
func handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	var paid, waiting, total int
	var income float64
	db.QueryRow("SELECT COUNT(*),COALESCE(SUM(money),0) FROM orders WHERE status=1").Scan(&paid, &income)
	db.QueryRow("SELECT COUNT(*) FROM orders WHERE status=0").Scan(&waiting)
	db.QueryRow("SELECT COUNT(*) FROM orders").Scan(&total)
	jsonResp(w, 200, "ok", map[string]interface{}{
		"total":   total,
		"paid":    paid,
		"waiting": waiting,
		"income":  fmt.Sprintf("%.2f", income),
	})
}

// /admin/orders — 订单列表
func handleAdminOrders(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	page, _ := strconv.Atoi(r.FormValue("page"))
	if page < 1 {
		page = 1
	}
	limit, offset := 20, (page-1)*20

	type orderRow struct {
		TradeNo    string
		OutTradeNo string
		PID        string
		Type       string
		Name       string
		Money      float64
		Status     int
		CreateTime time.Time
		PayTime    sql.NullTime
	}

	query := "SELECT trade_no,out_trade_no,pid,type,name,money,status,create_time,pay_time FROM orders WHERE 1=1"
	args := []interface{}{}
	if status := r.FormValue("status"); status != "" {
		query += " AND status=?"
		args = append(args, status)
	}
	if t := r.FormValue("type"); t != "" {
		query += " AND type=?"
		args = append(args, t)
	}
	if tradeNo := r.FormValue("trade_no"); tradeNo != "" {
		query += " AND trade_no LIKE ?"
		args = append(args, "%"+tradeNo+"%")
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		jsonResp(w, 500, "查询失败", nil)
		return
	}
	defer rows.Close()

	orders := []map[string]interface{}{}
	for rows.Next() {
		var o orderRow
		rows.Scan(&o.TradeNo, &o.OutTradeNo, &o.PID, &o.Type, &o.Name, &o.Money, &o.Status, &o.CreateTime, &o.PayTime)
		row := map[string]interface{}{
			"trade_no":     o.TradeNo,
			"out_trade_no": o.OutTradeNo,
			"pid":          o.PID,
			"type":         o.Type,
			"name":         o.Name,
			"money":        fmt.Sprintf("%.2f", o.Money),
			"status":       o.Status,
			"create_time":  o.CreateTime.Format("2006-01-02 15:04:05"),
		}
		if o.PayTime.Valid {
			row["pay_time"] = o.PayTime.Time.Format("2006-01-02 15:04:05")
		}
		orders = append(orders, row)
	}

	var total int
	db.QueryRow("SELECT COUNT(*) FROM orders").Scan(&total)
	jsonResp(w, 200, "ok", map[string]interface{}{
		"orders": orders,
		"total":  total,
		"pages":  (total + limit - 1) / limit,
	})
}

// /admin/order/create-test — 直接创建测试订单（跳过商户签名）
func handleAdminCreateTestOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, 400, "只支持POST", nil)
		return
	}
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}

	// 👇 关键修复：解析 multipart/form-data
	// 32 << 20 表示最大支持 32MB 上传
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		jsonResp(w, 400, "参数解析失败: "+err.Error(), nil)
		return
	}

	// 👇 下面这些不变，照常接收
	moneyStr := r.FormValue("money")
	money, _ := strconv.ParseFloat(moneyStr, 64)
	if money <= 0 {
		jsonResp(w, 400, "金额必须大于0", nil)
		return
	}
	typ := r.FormValue("type")
	if typ == "" {
		typ = "alipay"
	}
	name := r.FormValue("name")
	if name == "" {
		name = "测试商品"
	}
	outTradeNo := r.FormValue("out_trade_no")
	if outTradeNo == "" {
		outTradeNo = "TEST" + genTradeNo()
	}

	tradeNo := genTradeNo()
	now := time.Now()
	_, err = db.Exec(`INSERT INTO orders (trade_no,out_trade_no,pid,type,name,money,status,create_time) VALUES (?,?,?,?,?,?,?,?)`,
		tradeNo, outTradeNo, "1001", typ, name, money, OrderWaiting, now)
	if err != nil {
		log.Printf("[ERROR] 创建测试订单失败: %v", err)
		jsonResp(w, 500, "创建订单失败: "+err.Error(), nil)
		return
	}

	payURL := getSiteURL() + "/pay/" + tradeNo
	jsonResp(w, 200, "ok", map[string]interface{}{
		"trade_no": tradeNo,
		"pay_url":  payURL,
	})
}

// /admin/order/confirm — 确认收款
func handleAdminConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, 400, "只支持POST", nil)
		return
	}
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			jsonResp(w, 400, "参数解析失败", nil)
			return
		}
	}
	tradeNo := r.FormValue("trade_no")
	if tradeNo == "" {
		jsonResp(w, 400, "缺少订单号", nil)
		return
	}
	order, err := getOrderByTradeNo(tradeNo)
	if err != nil {
		jsonResp(w, 404, "订单不存在", nil)
		return
	}
	if order.Status != OrderWaiting {
		jsonResp(w, 400, "订单状态不是待支付", nil)
		return
	}
	if err := updateOrderPaid(order.TradeNo); err != nil {
		jsonResp(w, 500, "更新订单失败", nil)
		return
	}
	go notifyMerchant(order)
	jsonResp(w, 200, "确认成功", nil)
}

// /admin/order/test-pay — 测试支付（模拟付款）
func handleAdminTestPay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, 400, "只支持POST", nil)
		return
	}
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			jsonResp(w, 400, "参数解析失败", nil)
			return
		}
	}
	tradeNo := r.FormValue("trade_no")
	if tradeNo == "" {
		jsonResp(w, 400, "缺少订单号", nil)
		return
	}
	order, err := getOrderByTradeNo(tradeNo)
	if err != nil {
		jsonResp(w, 404, "订单不存在", nil)
		return
	}
	// 和 /submit.php 一样，跳转到支付页（不走网关，不通知商户）
	http.Redirect(w, r, getSiteURL()+"/pay/"+order.TradeNo, http.StatusFound)
}

// /admin/config — 获取配置
func handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	wxQR := getPaymentConfig("wxpay_qrcode")
	aliQR := getPaymentConfig("alipay_qrcode")
	qqQR := getPaymentConfig("qqpay_qrcode")
	if wxQR == "" {
		wxQR = cfg.WxpayQRCode
	}
	if aliQR == "" {
		aliQR = cfg.AlipayQRCode
	}
	if qqQR == "" {
		qqQR = cfg.QQpayQRCode
	}
	jsonResp(w, 200, "ok", map[string]interface{}{
		"wxpay_qrcode":  wxQR,
		"alipay_qrcode": aliQR,
		"qqpay_qrcode":  qqQR,
		"site_name":     getSiteName(),
		"site_url":      getSiteURL(),
	})
}

// /admin/config/save — 保存配置
func handleAdminConfigSave(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			jsonResp(w, 400, "参数解析失败", nil)
			return
		}
	}
	saveConfig := func(key, val string) {
		var cnt int
		db.QueryRow("SELECT COUNT(*) FROM payment_config WHERE cfg_key = ?", key).Scan(&cnt)
		if cnt > 0 {
			db.Exec("UPDATE payment_config SET cfg_value = ? WHERE cfg_key = ?", val, key)
		} else {
			db.Exec("INSERT INTO payment_config (cfg_key, cfg_value) VALUES (?, ?)", key, val)
		}
	}
	wxpay := r.PostForm.Get("wxpay_qrcode")
	alipay := r.PostForm.Get("alipay_qrcode")
	qqpay := r.PostForm.Get("qqpay_qrcode")
	siteName := r.PostForm.Get("site_name")
	siteURL := r.PostForm.Get("site_url")
	log.Printf("[ADMIN] 保存配置: wx=%q ali=%q qq=%q siteName=%q siteURL=%q", wxpay, alipay, qqpay, siteName, siteURL)
	saveConfig("wxpay_qrcode", wxpay)
	saveConfig("alipay_qrcode", alipay)
	saveConfig("qqpay_qrcode", qqpay)
	saveConfig("site_name", siteName)
	saveConfig("site_url", siteURL)
	log.Printf("[ADMIN] 配置已更新")
	jsonResp(w, 200, "保存成功", nil)
}

// /admin/merchant — 获取商户凭证
func handleAdminMerchant(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}
	merchant, err := getMerchant("1001")
	if err != nil {
		jsonResp(w, 500, "获取商户信息失败: "+err.Error(), nil)
		return
	}
	jsonResp(w, 200, "ok", map[string]interface{}{
		"pid":  merchant.PID,
		"name": merchant.Name,
		"key":  merchant.Key,
	})
}

func handleAdminMerchantSave(w http.ResponseWriter, r *http.Request) {
	if !getAdminSession(r) {
		jsonResp(w, 401, "未登录", nil)
		return
	}

	// ✅ 关键修复：必须用 ParseMultipartForm 解析 FormData
	// 不能用 ParseForm()
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB限制
		jsonResp(w, 400, "参数解析失败", err.Error())
		return
	}

	// ✅ 获取方式不变（兼容两种表单）
	newKey := strings.TrimSpace(r.FormValue("key"))

	if newKey == "" {
		jsonResp(w, 400, "密钥不能为空", nil)
		return
	}
	if len(newKey) < 8 {
		jsonResp(w, 400, "密钥长度至少8位", nil)
		return
	}

	// 执行更新
	db.Exec("UPDATE merchants SET `key` = ? WHERE pid = ?", newKey, "1001")
	log.Printf("[ADMIN] 商户密钥已更新: %s", newKey) // 日志可看到key，方便调试
	jsonResp(w, 200, "保存成功", nil)
}
