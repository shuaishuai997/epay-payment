package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/skip2/go-qrcode"

	_ "github.com/go-sql-driver/mysql"
)

type Config struct {
	SiteName      string
	SiteURL       string
	Key           string
	DBHost        string
	DBPort        int
	DBUser        string
	DBPassword    string
	DBName        string
	WxpayQRCode   string
	AlipayQRCode  string
	QQpayQRCode   string
	AdminUser     string
	AdminPassword string
}

var cfg *Config

func main() {
	cfg = loadConfig()
	if err := initDB(cfg); err != nil {
		log.Fatal("数据库初始化失败:", err)
	}

	http.HandleFunc("/submit.php", handleSubmit)
	http.HandleFunc("/notify.php", handleNotify)
	http.HandleFunc("/return.php", handleReturn)
	http.HandleFunc("/query.php", handleQuery)
	http.HandleFunc("/pay/", handlePayPage)
	http.HandleFunc("/pay/check", handlePayCheck)
	http.HandleFunc("/admin/login", handleAdminLogin)
	http.HandleFunc("/admin/logout", handleAdminLogout)
	http.HandleFunc("/admin/", handleAdminPage)
	http.HandleFunc("/admin/stats", handleAdminStats)
	http.HandleFunc("/admin/orders", handleAdminOrders)
	http.HandleFunc("/admin/order/confirm", handleAdminConfirm)
	http.HandleFunc("/admin/order/create-test", handleAdminCreateTestOrder)
	http.HandleFunc("/admin/order/test-pay", handleAdminTestPay)
	http.HandleFunc("/admin/config", handleAdminConfig)
	http.HandleFunc("/admin/config/save", handleAdminConfigSave)
	http.HandleFunc("/admin/merchant", handleAdminMerchant)
	http.HandleFunc("/admin/merchant/save", handleAdminMerchantSave)
	http.HandleFunc("/admin/debug-config", handleDebugConfig)

	log.Printf("支付平台启动: %s", cfg.SiteURL)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func loadConfig() *Config {
	return &Config{
		SiteName:      "DevPay",
		SiteURL:       "http://localhost:8080",
		Key:           "fanshuai11",
		DBHost:        "localhost",
		DBPort:        3306,
		DBUser:        "root",
		DBPassword:    "root",
		DBName:        "payment",
		WxpayQRCode:   "wxp://xxx",
		AlipayQRCode:  "https://qr.alipay.com/xxx",
		QQpayQRCode:   "https://i.qpay.qq.com/xxx",
		AdminUser:     "admin",
		AdminPassword: "admin123",
	}
}

func getPaymentConfig(key string) string {
	var val string
	db.QueryRow("SELECT cfg_value FROM payment_config WHERE cfg_key = ?", key).Scan(&val)
	return val
}

func (c *Config) getQRCode(t string) string {
	switch t {
	case "wxpay":
		val := getPaymentConfig("wxpay_qrcode")
		if val != "" {
			return val
		}
		return c.WxpayQRCode
	case "alipay":
		val := getPaymentConfig("alipay_qrcode")
		if val != "" {
			return val
		}
		return c.AlipayQRCode
	case "qqpay":
		val := getPaymentConfig("qqpay_qrcode")
		if val != "" {
			return val
		}
		return c.QQpayQRCode
	default:
		val := getPaymentConfig("wxpay_qrcode")
		if val != "" {
			return val
		}
		return c.WxpayQRCode
	}
}

type Merchant struct {
	ID      int64
	PID     string
	Name    string
	Key     string
	Status  int
	Balance float64
}

type Order struct {
	ID          int64
	TradeNo     string
	OutTradeNo  string
	PID         string
	Type        string
	Name        string
	Money       float64
	Status      int
	NotifyURL   string
	ReturnURL   string
	IP          string
	CreateTime  time.Time
	PayTime     *time.Time
	NotifyCount int
}

const (
	OrderWaiting  = 0
	OrderPaid     = 1
	OrderClosed   = 2
	OrderRefunded = 3
)

var db *sql.DB

func initDB(c *Config) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(10)

	db.Exec(`CREATE TABLE IF NOT EXISTS merchants (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		pid VARCHAR(32) NOT NULL UNIQUE,
		name VARCHAR(100) NOT NULL,
		key VARCHAR(64) NOT NULL,
		status TINYINT DEFAULT 1,
		balance DECIMAL(12,2) DEFAULT 0,
		create_time DATETIME DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		trade_no VARCHAR(64) NOT NULL UNIQUE,
		out_trade_no VARCHAR(64) NOT NULL,
		pid VARCHAR(32) NOT NULL DEFAULT '1001',
		type VARCHAR(20) NOT NULL,
		name VARCHAR(200),
		money DECIMAL(10,2) NOT NULL,
		status TINYINT DEFAULT 0,
		notify_url VARCHAR(500),
		return_url VARCHAR(500),
		ip VARCHAR(45),
		create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		pay_time DATETIME,
		notify_count INT DEFAULT 0,
		notify_time DATETIME
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	addCol := func(col, def string) {
		var cnt int
		db.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
			WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='orders' AND COLUMN_NAME=?`, col).Scan(&cnt)
		if cnt == 0 {
			log.Printf("[MIGRATE] 添加缺失列: %s", col)
			db.Exec(fmt.Sprintf(`ALTER TABLE orders ADD COLUMN %s %s`, col, def))
		}
	}
	addCol("out_trade_no", "VARCHAR(64) NOT NULL DEFAULT ''")
	addCol("pid", "VARCHAR(32) NOT NULL DEFAULT '1001'")
	addCol("type", "VARCHAR(20) NOT NULL DEFAULT 'alipay'")
	addCol("name", "VARCHAR(200)")
	addCol("money", "DECIMAL(10,2) NOT NULL DEFAULT 0")
	addCol("status", "TINYINT DEFAULT 0")
	addCol("notify_url", "VARCHAR(500)")
	addCol("return_url", "VARCHAR(500)")
	addCol("ip", "VARCHAR(45)")
	addCol("pay_time", "DATETIME")
	addCol("notify_count", "INT DEFAULT 0")
	addCol("notify_time", "DATETIME")

	dropCol := func(col string) {
		var cnt int
		db.QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
			WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='orders' AND COLUMN_NAME=?`, col).Scan(&cnt)
		if cnt > 0 {
			log.Printf("[MIGRATE] 删除旧列: %s", col)
			db.Exec(fmt.Sprintf(`ALTER TABLE orders DROP COLUMN %s`, col))
		}
	}
	dropCol("amount")
	dropCol("payment_type")

	// 初始化支付配置表
	db.Exec(`CREATE TABLE IF NOT EXISTS payment_config (
		id INT AUTO_INCREMENT PRIMARY KEY,
		cfg_key VARCHAR(64) NOT NULL UNIQUE,
		cfg_value TEXT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	// 插入默认收款码配置（如果不存在）
	if c.WxpayQRCode != "" {
		db.Exec(`INSERT IGNORE INTO payment_config (cfg_key, cfg_value) VALUES (?, ?)`, "wxpay_qrcode", c.WxpayQRCode)
	}
	if c.AlipayQRCode != "" {
		db.Exec(`INSERT IGNORE INTO payment_config (cfg_key, cfg_value) VALUES (?, ?)`, "alipay_qrcode", c.AlipayQRCode)
	}
	if c.QQpayQRCode != "" {
		db.Exec(`INSERT IGNORE INTO payment_config (cfg_key, cfg_value) VALUES (?, ?)`, "qqpay_qrcode", c.QQpayQRCode)
	}

	db.Exec("INSERT IGNORE INTO merchants (pid, name, `key`) VALUES (?, ?, ?)",
		"1001", "默认商户", c.Key)

	return nil
}

func getMerchant(pid string) (*Merchant, error) {
	m := &Merchant{}
	err := db.QueryRow("SELECT id, pid, name, `key`, status, balance FROM merchants WHERE pid = ?", pid).
		Scan(&m.ID, &m.PID, &m.Name, &m.Key, &m.Status, &m.Balance)
	return m, err
}

func getOrderByTradeNo(tradeNo string) (*Order, error) {
	o := &Order{}
	var notifyURL, returnURL, ip sql.NullString
	var payTime sql.NullTime
	err := db.QueryRow(`SELECT id, trade_no, out_trade_no, pid, type, name, money, status,
		notify_url, return_url, ip, create_time, pay_time FROM orders WHERE trade_no = ?`, tradeNo).
		Scan(&o.ID, &o.TradeNo, &o.OutTradeNo, &o.PID, &o.Type, &o.Name, &o.Money, &o.Status,
			&notifyURL, &returnURL, &ip, &o.CreateTime, &payTime)
	if err != nil {
		log.Printf("[ERROR] getOrderByTradeNo(%s): %v", tradeNo, err)
		return nil, err
	}
	if notifyURL.Valid {
		o.NotifyURL = notifyURL.String
	}
	if returnURL.Valid {
		o.ReturnURL = returnURL.String
	}
	if ip.Valid {
		o.IP = ip.String
	}
	if payTime.Valid {
		o.PayTime = &payTime.Time
	}
	return o, nil
}

func getOrderByOutTradeNo(outTradeNo, pid string) (*Order, error) {
	o := &Order{}
	var notifyURL, returnURL, ip sql.NullString
	var payTime sql.NullTime
	err := db.QueryRow(`SELECT id, trade_no, out_trade_no, pid, type, name, money, status,
		notify_url, return_url, ip, create_time, pay_time FROM orders WHERE out_trade_no = ? AND pid = ?`,
		outTradeNo, pid).
		Scan(&o.ID, &o.TradeNo, &o.OutTradeNo, &o.PID, &o.Type, &o.Name, &o.Money, &o.Status,
			&notifyURL, &returnURL, &ip, &o.CreateTime, &payTime)
	if err != nil {
		return nil, err
	}
	if notifyURL.Valid {
		o.NotifyURL = notifyURL.String
	}
	if returnURL.Valid {
		o.ReturnURL = returnURL.String
	}
	if ip.Valid {
		o.IP = ip.String
	}
	if payTime.Valid {
		o.PayTime = &payTime.Time
	}
	return o, nil
}

func createOrder(o *Order) error {
	_, err := db.Exec(`INSERT INTO orders (trade_no, out_trade_no, pid, type, name, money, status,
		notify_url, return_url, ip, create_time) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		o.TradeNo, o.OutTradeNo, o.PID, o.Type, o.Name, o.Money, o.NotifyURL, o.ReturnURL, o.IP, time.Now())
	return err
}

func updateOrderPaid(tradeNo string) error {
	_, err := db.Exec("UPDATE orders SET status = 1, pay_time = ? WHERE trade_no = ? AND status = 0",
		time.Now(), tradeNo)
	return err
}

func updateOrderNotifyCount(tradeNo string) {
	db.Exec("UPDATE orders SET notify_count = notify_count + 1, notify_time = ? WHERE trade_no = ?",
		time.Now(), tradeNo)
}

func getReturnURL(tradeNo string, out *string) {
	db.QueryRow("SELECT return_url FROM orders WHERE trade_no = ?", tradeNo).Scan(out)
}

func getReturnURLByOut(outTradeNo, pid string, out *string) {
	db.QueryRow("SELECT return_url FROM orders WHERE out_trade_no = ? AND pid = ?", outTradeNo, pid).Scan(out)
}

// CreateUrlString 生成待签名字符串, ["a", "b", "c"], ["d", "e", "f"] => "a=d&b=e&c=f"
func CreateUrlString(keys []string, values []string) string {
	urlString := ""
	for i, key := range keys {
		urlString += key + "=" + values[i] + "&"
	}
	// trim 掉最后的 &
	return strings.TrimSuffix(urlString, "&")
}

// MD5String 生成 加盐(商户 key) MD5 字符串
func MD5String(urlString string, key string) string {
	digest := md5.Sum([]byte(urlString + key))
	return fmt.Sprintf("%x", digest)
}

// ParamsFilter 过滤参数，生成签名时需删除 “sign” 和 “sign_type” 参数
func ParamsFilter(params map[string]string) map[string]string {
	return lo.PickBy(params, func(key string, value string) bool {
		return !(key == "sign" || key == "sign_type" || value == "")
	})
}

// ParamsSort 对参数进行排序，返回排序后的 keys 和 values （go 中 map 为乱序）
func ParamsSort(params map[string]string) ([]string, []string) {
	keys := lo.Keys(params)
	sort.Strings(keys)

	values := lo.Map(keys, func(key string, i int) string {
		return params[key]
	})

	return keys, values
}
func verifySign(params map[string]string, sign, key string) bool {
	filtered := ParamsFilter(params)
	keys, values := ParamsSort(filtered)
	getsign := MD5String(CreateUrlString(keys, values), key)
	return strings.EqualFold(getsign, sign)
}

func trim(s string) string { return strings.TrimSpace(s) }

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}

func genTradeNo() string {
	now := time.Now()
	return fmt.Sprintf("SP%s%04d", now.Format("20060102150405"), now.Nanosecond()/100000)
}

func getPayTypeName(t string) string {
	switch t {
	case "wxpay":
		return "微信支付"
	case "alipay":
		return "支付宝"
	case "qqpay":
		return "QQ钱包"
	default:
		return "在线支付"
	}
}

func getPayTypeIcon(t string) string {
	switch t {
	case "wxpay":
		return "💚"
	case "alipay":
		return "💙"
	case "qqpay":
		return "🐧"
	default:
		return "💰"
	}
}

func statusText(s int) string {
	switch s {
	case OrderWaiting:
		return "WAITING"
	case OrderPaid:
		return "TRADE_SUCCESS"
	case OrderClosed:
		return "TRADE_CLOSED"
	case OrderRefunded:
		return "TRADE_REFUND"
	default:
		return "UNKNOWN"
	}
}

func jsonResp(w http.ResponseWriter, code int, msg string, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"code": code, "msg": msg, "data": data})
}

func showError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>错误</title>
<style>body{font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#f5f5f5}
.card{background:#fff;padding:40px;border-radius:12px;box-shadow:0 2px 12px rgba(0,0,0,.1);text-align:center;max-width:400px}
h2{color:#e74c3c;margin:0 0 10px}p{color:#666;margin:0}</style></head>
<body><div class="card"><h2>❌ 支付错误</h2><p>%s</p></div></body></html>`, msg)
}

func handleSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	pid := trim(r.FormValue("pid"))
	ptype := trim(r.FormValue("type"))
	outTradeNo := trim(r.FormValue("out_trade_no"))
	notifyURL := trim(r.FormValue("notify_url"))
	returnURL := trim(r.FormValue("return_url"))
	name := trim(r.FormValue("name"))
	money := trim(r.FormValue("money"))
	sign := trim(r.FormValue("sign"))

	if pid == "" || ptype == "" || outTradeNo == "" || money == "" || sign == "" {
		showError(w, "参数不完整")
		return
	}

	amount, err := strconv.ParseFloat(money, 64)
	if err != nil || amount <= 0 {
		showError(w, "金额格式错误")
		return
	}

	merchant, err := getMerchant(pid)
	if err != nil || merchant.Status != 1 {
		showError(w, "商户不存在或已禁用")
		return
	}

	// params := map[string]string{
	// 	"pid": pid, "type": ptype, "out_trade_no": outTradeNo,
	// 	"notify_url": notifyURL, "return_url": returnURL, "name": name, "money": money,
	// }

	// if !verifySign(params, sign, merchant.Key) {
	// 	showError(w, "签名验证失败")
	// 	return
	// }

	exist, _ := getOrderByOutTradeNo(outTradeNo, pid)
	if exist != nil {
		if exist.Status == OrderPaid && exist.ReturnURL != "" {
			http.Redirect(w, r, exist.ReturnURL, http.StatusFound)
			return
		}
		http.Redirect(w, r, cfg.SiteURL+"/pay/"+exist.TradeNo, http.StatusFound)
		return
	}

	order := &Order{
		TradeNo: genTradeNo(), OutTradeNo: outTradeNo, PID: pid,
		Type: ptype, Name: name, Money: amount,
		NotifyURL: notifyURL, ReturnURL: returnURL,
		IP: getClientIP(r),
	}
	if err := createOrder(order); err != nil {
		showError(w, "创建订单失败: "+err.Error())
		return
	}

	http.Redirect(w, r, cfg.SiteURL+"/pay/"+order.TradeNo, http.StatusFound)
}

func handlePayPage(w http.ResponseWriter, r *http.Request) {
	tradeNo := strings.TrimPrefix(r.URL.Path, "/pay/")
	if tradeNo == "" {
		showError(w, "订单号不存在")
		return
	}

	order, err := getOrderByTradeNo(tradeNo)
	if err != nil {
		showError(w, "订单不存在")
		return
	}

	if order.Status == OrderPaid {
		if order.ReturnURL != "" {
			http.Redirect(w, r, order.ReturnURL, http.StatusFound)
			return
		}
		showError(w, "订单已支付")
		return
	}

	qrURL := cfg.getQRCode(order.Type)
	qrBase64 := ""
	qrError := ""
	log.Printf("[PayPage] trade_no=%s type=%s qrURL=%q", order.TradeNo, order.Type, qrURL)

	if qrURL == "" {
		qrError = "请先在后台配置" + getPayTypeName(order.Type) + "收款码"
	} else {
		png, err := qrcode.Encode(qrURL, qrcode.Medium, 200)
		if err != nil {
			qrError = "二维码生成失败: " + err.Error()
		} else {
			qrBase64 = base64.StdEncoding.EncodeToString(png)
		}
	}

	data := map[string]interface{}{
		"TradeNo":     order.TradeNo,
		"OrderName":   order.Name,
		"Money":       fmt.Sprintf("%.2f", order.Money),
		"PayType":     getPayTypeName(order.Type),
		"PayTypeIcon": getPayTypeIcon(order.Type),
		"QRBase64":    qrBase64,
		"QRError":     qrError,
		"SiteName":    cfg.SiteName,
	}

	tmpl, err := template.ParseFiles("templates/pay.html")
	if err != nil {
		showError(w, "模板加载失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, data)
}

func handlePayCheck(w http.ResponseWriter, r *http.Request) {
	tradeNo := r.URL.Query().Get("trade_no")
	if tradeNo == "" {
		jsonResp(w, 400, "缺少订单号", nil)
		return
	}

	order, err := getOrderByTradeNo(tradeNo)
	if err != nil {
		jsonResp(w, 404, "订单不存在", nil)
		return
	}

	jsonResp(w, 200, "ok", map[string]interface{}{
		"status":     order.Status,
		"trade_no":   order.TradeNo,
		"return_url": order.ReturnURL,
	})
}

func handleReturn(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	tradeNo := trim(r.FormValue("trade_no"))
	outTradeNo := trim(r.FormValue("out_trade_no"))

	var returnURL string
	if tradeNo != "" {
		getReturnURL(tradeNo, &returnURL)
	} else if outTradeNo != "" {
		getReturnURLByOut(outTradeNo, trim(r.FormValue("pid")), &returnURL)
	}

	if returnURL != "" {
		u, _ := url.Parse(returnURL)
		if u != nil {
			q := u.Query()
			q.Set("trade_no", tradeNo)
			q.Set("out_trade_no", outTradeNo)
			q.Set("trade_status", "TRADE_SUCCESS")
			u.RawQuery = q.Encode()
			returnURL = u.String()
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
		return
	}
	showError(w, "缺少跳转地址")
}

func handleNotify(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("success"))
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	pid := trim(r.FormValue("pid"))
	outTradeNo := trim(r.FormValue("out_trade_no"))
	sign := trim(r.FormValue("sign"))

	if pid == "" || outTradeNo == "" || sign == "" {
		jsonResp(w, 400, "参数不完整", nil)
		return
	}

	merchant, err := getMerchant(pid)
	if err != nil {
		jsonResp(w, 400, "商户不存在", nil)
		return
	}

	if !verifySign(map[string]string{"pid": pid, "out_trade_no": outTradeNo}, sign, merchant.Key) {
		jsonResp(w, 401, "签名验证失败", nil)
		return
	}

	order, err := getOrderByOutTradeNo(outTradeNo, pid)
	if err != nil {
		jsonResp(w, 404, "订单不存在", nil)
		return
	}

	data := map[string]interface{}{
		"pid":          pid,
		"trade_no":     order.TradeNo,
		"out_trade_no": order.OutTradeNo,
		"type":         order.Type,
		"name":         order.Name,
		"money":        fmt.Sprintf("%.2f", order.Money),
		"trade_status": statusText(order.Status),
		"create_time":  order.CreateTime.Format("2006-01-02 15:04:05"),
	}
	if order.PayTime != nil {
		data["pay_time"] = order.PayTime.Format("2006-01-02 15:04:05")
	}

	jsonResp(w, 200, "success", data)
}

func notifyMerchant(order *Order) {
	if order.NotifyURL == "" {
		return
	}

	merchant, _ := getMerchant(order.PID)
	if merchant == nil {
		return
	}

	params := map[string]string{
		"pid": order.PID, "trade_no": order.TradeNo, "out_trade_no": order.OutTradeNo,
		"type": order.Type, "name": order.Name,
		"money": fmt.Sprintf("%.2f", order.Money), "trade_status": "TRADE_SUCCESS",
	}
	params["sign"] = func(params map[string]string, key string) string {
		filtered := ParamsFilter(params)
		keys, values := ParamsSort(filtered)
		return MD5String(CreateUrlString(keys, values), key)
	}(params, merchant.Key)
	params["sign_type"] = "MD5"

	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}

	for i := 0; i < 3; i++ {
		resp, err := http.PostForm(order.NotifyURL, values)
		if err != nil {
			time.Sleep(time.Duration(i+1) * 3 * time.Second)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			updateOrderNotifyCount(order.TradeNo)
			log.Printf("通知商户成功: %s", order.OutTradeNo)
			return
		}
		time.Sleep(time.Duration(i+1) * 3 * time.Second)
	}
	log.Printf("通知商户失败: %s", order.OutTradeNo)
}

// /admin/debug-config — 诊断：直接读数据库返回配置值
func handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	wx := getPaymentConfig("wxpay_qrcode")
	ali := getPaymentConfig("alipay_qrcode")
	qq := getPaymentConfig("qqpay_qrcode")
	log.Printf("[DEBUG] getPaymentConfig -> wx=%q ali=%q qq=%q", wx, ali, qq)
	jsonResp(w, 200, "ok", map[string]interface{}{
		"wxpay_qrcode":  wx,
		"alipay_qrcode": ali,
		"qqpay_qrcode":  qq,
	})
}
