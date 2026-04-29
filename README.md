# 易支付协议支付系统

基于 Go 语言实现的易支付协议支付系统。

## 功能特性

- 支持易支付标准协议
- 支持多种支付方式（支付宝、微信、QQ支付）
- MD5签名验证
- 异步通知处理
- 订单查询接口
- MySQL 数据持久化

## 接口说明

### 1. 提交支付 `/submit.php` (POST)

**请求参数:**

| 参数 | 必填 | 说明 |
|------|------|------|
| type | 是 | 支付方式: alipay, wxpay, qqpay |
| out_trade_no | 是 | 商户订单号 |
| amount | 是 | 支付金额 |
| name | 是 | 商品名称 |
| notify_url | 否 | 异步通知地址 |
| return_url | 否 | 同步跳转地址 |
| sign | 是 | MD5签名 |
| sign_type | 是 | 签名类型，固定为 MD5 |

**签名规则:**
1. 将所有非空参数按参数名排序
2. 拼接成 `key=value&key2=value2` 格式
3. 在末尾追加商户密钥
4. 进行 MD5 加密（32位小写）

**响应示例:**
```json
{
  "code": 200,
  "msg": "success",
  "data": {
    "trade_no": "EP20240128001",
    "pay_url": "https://pay.example.com/submit.php?pid=1001&type=wxpay&...",
    "qrcode": "https://pay.example.com/submit.php?pid=1001&type=wxpay&..."
  }
}
```

### 2. 异步通知 `/notify.php`

支付成功后，易支付会向商户服务器发送异步通知。

**通知参数:**

| 参数 | 说明 |
|------|------|
| trade_no | 易支付订单号 |
| out_trade_no | 商户订单号 |
| trade_status | 交易状态 (TRADE_SUCCESS) |
| money | 支付金额 |
| sign | 签名 |

**响应:**
- 验证成功返回 `success`
- 验证失败返回 `fail`

### 3. 同步跳转 `/return.php`

用户支付完成后跳转回商户页面。

### 4. 查询订单 `/query.php` (GET)

**请求参数:**

| 参数 | 必填 | 说明 |
|------|------|------|
| out_trade_no | 是 | 商户订单号 |
| sign | 是 | 签名 |

**响应示例:**
```json
{
  "code": 200,
  "msg": "success",
  "data": {
    "out_trade_no": "20240128001",
    "trade_no": "EP20240128001",
    "amount": 10.00,
    "status": 1,
    "create_time": "2024-01-28 10:00:00",
    "pay_time": "2024-01-28 10:05:00"
  }
}
```

## 数据库配置

系统使用 MySQL 数据库，需要创建数据库:

```sql
CREATE DATABASE payment DEFAULT CHARSET utf8mb4;
```

订单表会自动创建。

## 配置说明

在 `main.go` 中修改 `loadConfig()` 函数的配置:

```go
return &EpayConfig{
    APIUrl:     "https://pay.example.com/submit.php",  // 易支付网关
    PID:        "1001",                                 // 商户ID
    Key:        "your_secret_key_here",                 // 商户密钥
    NotifyURL:  "https://yourdomain.com/notify.php",    // 异步通知地址
    ReturnURL:  "https://yourdomain.com/return.php",    // 同步跳转地址
    DBHost:     "localhost",
    DBPort:     3306,
    DBUser:     "root",
    DBPassword: "password",
    DBName:     "payment",
}
```

## 运行

```bash
# 进入项目目录
cd I:\code\epay-payment

# 下载依赖
go mod tidy

# 运行服务
go run main.go
```

服务将在 `:8080` 端口启动。

## 请求示例

```bash
# 提交支付
curl -X POST http://localhost:8080/submit.php \
  -H "Content-Type: application/json" \
  -d '{
    "type": "wxpay",
    "out_trade_no": "ORDER20240128001",
    "amount": "10.00",
    "name": "测试商品",
    "sign": "计算出的MD5签名",
    "sign_type": "MD5"
  }'

# 查询订单
curl "http://localhost:8080/query.php?out_trade_no=ORDER20240128001&sign=计算出的MD5签名"
```

## 签名计算示例

```go
// 参数
params := map[string]string{
    "type":         "wxpay",
    "out_trade_no": "ORDER20240128001",
    "amount":       "10.00",
    "name":         "测试商品",
}

// 排序后拼接: amount=10.00&name=测试商品&out_trade_no=ORDER20240128001&type=wxpay
// 追加密钥: amount=10.00&name=测试商品&out_trade_no=ORDER20240128001&type=wxpayyour_secret_key_here
// MD5加密得到签名
```

## 订单状态

| 状态码 | 说明 |
|-------|------|
| 0 | 待支付 |
| 1 | 已支付 |
| 2 | 已关闭 |

## 注意事项

1. 生产环境请使用 HTTPS
2. 密钥请妥善保管，不要硬编码在代码中
3. 异步通知需要外网可访问
4. 建议添加 IP 白名单验证
5. 建议添加请求频率限制
