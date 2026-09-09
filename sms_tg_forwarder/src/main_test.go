package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAppriseSender(t *testing.T) {
	var receivedPayload apprisePayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json Content-Type, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("failed to unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	sender := &appriseSender{
		apiURL:  server.URL + "/notify/sms",
		title:   "收到来自 {{from}} 的短信", // 模拟配置文件中原本带“短信”字样的标题
		msgType: "info",
		format:  "text",
		client:  server.Client(),
	}

	// 1. 测试短信推送
	sms := smsRecord{
		ID:      1,
		Address: "10086",
		Body:    "您的余额为 100 元",
		Type:    "sms",
	}

	err := sender.SendSMS(context.Background(), sms)
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}

	if receivedPayload.Title != "收到来自 10086 的短信" {
		t.Errorf("expected title '收到来自 10086 的短信', got '%s'", receivedPayload.Title)
	}
	if !strings.Contains(receivedPayload.Body, "【收到短信】") || !strings.Contains(receivedPayload.Body, "10086") {
		t.Errorf("unexpected body: %s", receivedPayload.Body)
	}

	// 2. 测试通话推送（即使 title 写了“短信”，也必须自动修正为未接来电，绝不能出现“短信”！）
	call := smsRecord{
		ID:      2,
		Address: "13800138000",
		Body:    "未接来电 (响铃 18 秒)",
		Type:    "call",
		Time:    time.Date(2026, 3, 9, 18, 0, 0, 0, time.Local),
	}

	err = sender.SendSMS(context.Background(), call)
	if err != nil {
		t.Fatalf("SendSMS call failed: %v", err)
	}

	if strings.Contains(receivedPayload.Title, "短信") {
		t.Errorf("call title must NOT contain '短信', got '%s'", receivedPayload.Title)
	}
	if !strings.Contains(receivedPayload.Title, "未接来电") || !strings.Contains(receivedPayload.Title, "13800138000") {
		t.Errorf("expected call title to contain '未接来电' and number, got '%s'", receivedPayload.Title)
	}
	if !strings.Contains(receivedPayload.Body, "【未接来电】") || !strings.Contains(receivedPayload.Body, "13800138000") {
		t.Errorf("unexpected call body: %s", receivedPayload.Body)
	}
}

func TestWebhookSenderPostJSON(t *testing.T) {
	type CustomWebhookPayload struct {
		Sender string `json:"sender"`
		Msg    string `json:"msg"`
	}

	var receivedPayload CustomWebhookPayload
	var authHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("failed to unmarshal JSON body: %v (body was %s)", err, string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := &webhookSender{
		rawURL:       server.URL,
		method:       "POST",
		headers:      "Authorization: Bearer secret-token\nContent-Type: application/json",
		bodyTemplate: `{"sender":"{{from}}","msg":"{{body}}"}`,
		client:       server.Client(),
	}

	record := smsRecord{
		ID:      2,
		Address: "+8613800138000",
		Body:    "验证码是: \"123456\"\n请勿泄露给他人",
		Type:    "sms",
	}

	err := sender.SendSMS(context.Background(), record)
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}

	if authHeader != "Bearer secret-token" {
		t.Errorf("expected 'Bearer secret-token', got '%s'", authHeader)
	}
	if receivedPayload.Sender != "+8613800138000" {
		t.Errorf("expected sender '+8613800138000', got '%s'", receivedPayload.Sender)
	}
	if receivedPayload.Msg != "验证码是: \"123456\"\n请勿泄露给他人" {
		t.Errorf("expected body to match with quotes and newlines, got '%s'", receivedPayload.Msg)
	}
}

func TestWebhookSenderGet(t *testing.T) {
	var requestedURI string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		requestedURI = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := &webhookSender{
		rawURL: server.URL + "/push?sender={{from}}&text={{body}}&type={{type}}",
		method: "GET",
		client: server.Client(),
	}

	record := smsRecord{
		ID:      3,
		Address: "10010",
		Body:    "hello world",
		Type:    "sms",
	}

	err := sender.SendSMS(context.Background(), record)
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}

	expectedURI := "/push?sender=10010&text=hello+world&type=%E7%9F%AD%E4%BF%A1"
	if requestedURI != expectedURI {
		t.Errorf("expected '%s', got '%s'", expectedURI, requestedURI)
	}
}

func TestBarkSender(t *testing.T) {
	var receivedPayload barkPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/push" {
			t.Errorf("expected /push path, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("unmarshal bark payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"code": 200, "message": "success"}`))
	}))
	defer server.Close()

	sender := &barkSender{
		serverURL: server.URL,
		deviceKey: "test_device_key",
		group:     "测试分组",
		sound:     "minuet",
		icon:      "https://example.com/icon.png",
		client:    server.Client(),
	}

	// 1. Test SMS
	sms := smsRecord{
		ID:      10,
		Address: "10086",
		Body:    "您的流量剩余 5GB",
		Type:    "sms",
	}
	if err := sender.SendSMS(context.Background(), sms); err != nil {
		t.Fatalf("send bark sms failed: %v", err)
	}
	if receivedPayload.Title != "📩 短信来自: 10086" {
		t.Errorf("expected title '📩 短信来自: 10086', got '%s'", receivedPayload.Title)
	}
	if receivedPayload.Group != "测试分组" {
		t.Errorf("expected group '测试分组', got '%s'", receivedPayload.Group)
	}

	// 2. Test Call
	call := smsRecord{
		ID:      20,
		Address: "张三 (13800138000)",
		Body:    "未接来电 (响铃 15 秒)",
		Type:    "call",
		Time:    time.Date(2026, 3, 9, 14, 30, 0, 0, time.Local),
	}
	if err := sender.SendSMS(context.Background(), call); err != nil {
		t.Fatalf("send bark call failed: %v", err)
	}
	if receivedPayload.Title != "📞 未接来电: 张三 (13800138000)" {
		t.Errorf("expected call title, got '%s'", receivedPayload.Title)
	}
	if receivedPayload.Group != "未接来电" {
		t.Errorf("expected call group '未接来电', got '%s'", receivedPayload.Group)
	}
}

func TestWechatSender(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	sender := &wechatSender{
		webhookURL: server.URL,
		client:     server.Client(),
	}

	// 1. Test SMS
	sms := smsRecord{
		ID:      101,
		Address: "10010",
		Body:    "验证码是 998877",
		Type:    "sms",
		Time:    time.Date(2026, 3, 9, 15, 0, 0, 0, time.Local),
	}
	if err := sender.SendSMS(context.Background(), sms); err != nil {
		t.Fatalf("send wechat sms: %v", err)
	}
	textMap := receivedBody["text"].(map[string]any)
	content := textMap["content"].(string)
	if !strings.Contains(content, "【收到短信通知】") || !strings.Contains(content, "10010") {
		t.Errorf("unexpected wechat content: %s", content)
	}

	// 2. Test Call
	call := smsRecord{
		ID:      102,
		Address: "13912345678",
		Body:    "未接来电 (响铃 20 秒)",
		Type:    "call",
		Time:    time.Date(2026, 3, 9, 15, 5, 0, 0, time.Local),
	}
	if err := sender.SendSMS(context.Background(), call); err != nil {
		t.Fatalf("send wechat call: %v", err)
	}
	textMap = receivedBody["text"].(map[string]any)
	content = textMap["content"].(string)
	if !strings.Contains(content, "【未接来电通知】") || !strings.Contains(content, "13912345678") {
		t.Errorf("unexpected wechat call content: %s", content)
	}
}

type recordingSender struct {
	sent []smsRecord
}

func (r *recordingSender) SendSMS(ctx context.Context, record smsRecord) error {
	r.sent = append(r.sent, record)
	return nil
}

func TestProcessNewCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "calllog_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "calllog.db")
	lastCallPath := filepath.Join(tmpDir, "last_call.txt")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create calls table
	_, err = db.Exec(`
		CREATE TABLE calls (
			_id INTEGER PRIMARY KEY AUTOINCREMENT,
			number TEXT,
			date INTEGER,
			duration INTEGER,
			type INTEGER,
			name TEXT
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert existing history call
	_, err = db.Exec(`
		INSERT INTO calls (number, date, duration, type, name)
		VALUES ('10086', 1773000000000, 0, 3, '中国移动');
	`)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize cursor: should point to latest history (ID 1)
	lastCallID, err := loadOrInitializeLastCallID(context.Background(), db, lastCallPath)
	if err != nil {
		t.Fatal(err)
	}
	if lastCallID != 1 {
		t.Fatalf("expected lastCallID=1, got %d", lastCallID)
	}

	rec := &recordingSender{}

	// Process without new calls: should find nothing
	nextID, err := processNewCalls(context.Background(), db, rec, lastCallPath, lastCallID, []int{3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if nextID != 1 || len(rec.sent) != 0 {
		t.Fatalf("expected 0 new calls, got %d", len(rec.sent))
	}

	// Insert new missed call (type 3) and outgoing call (type 2)
	_, err = db.Exec(`
		INSERT INTO calls (number, date, duration, type, name)
		VALUES ('13800000001', 1773000100000, 12, 3, '张三');
	`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		INSERT INTO calls (number, date, duration, type, name)
		VALUES ('13800000002', 1773000200000, 60, 2, '李四');
	`)
	if err != nil {
		t.Fatal(err)
	}

	// Process again: should only forward type 3 (missed call)
	nextID, err = processNewCalls(context.Background(), db, rec, lastCallPath, lastCallID, []int{3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if nextID != 3 {
		t.Fatalf("expected nextID=3, got %d", nextID)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("expected 1 forwarded call, got %d", len(rec.sent))
	}

	forwarded := rec.sent[0]
	if forwarded.Type != "call" {
		t.Errorf("expected Type='call', got '%s'", forwarded.Type)
	}
	if forwarded.Address != "张三 (13800000001)" {
		t.Errorf("expected Address='张三 (13800000001)', got '%s'", forwarded.Address)
	}
	if !strings.Contains(forwarded.Body, "未接来电") || !strings.Contains(forwarded.Body, "12") {
		t.Errorf("expected Body to describe missed call with duration, got '%s'", forwarded.Body)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	os.Clearenv()

	_, _, err := loadConfig("")
	if err == nil {
		t.Errorf("expected error when no destination is configured")
	}

	// 1. Set Bark Key
	os.Setenv("BARK_KEY", "my-bark-key")
	cfg, _, err := loadConfig("")
	if err != nil {
		t.Fatalf("unexpected error with Bark key: %v", err)
	}
	if cfg.BarkKey != "my-bark-key" {
		t.Errorf("expected BarkKey 'my-bark-key', got '%s'", cfg.BarkKey)
	}
	if cfg.LastMsgPath != defaultLastMsgPath {
		t.Errorf("expected default LastMsgPath, got %s", cfg.LastMsgPath)
	}
	if cfg.LastCallPath != defaultLastCallPath {
		t.Errorf("expected default LastCallPath, got %s", cfg.LastCallPath)
	}

	// 2. Set Wechat Webhook
	os.Clearenv()
	os.Setenv("WECHAT_WEBHOOK", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test")
	cfg, _, err = loadConfig("")
	if err != nil {
		t.Fatalf("unexpected error with Wechat webhook: %v", err)
	}
	if cfg.WechatWebhook != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test" {
		t.Errorf("expected WechatWebhook to match")
	}

	// 3. Set Webhook
	os.Clearenv()
	os.Setenv("WEBHOOK_URL", "https://api.example.com/webhook")
	cfg, _, err = loadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WebhookURL != "https://api.example.com/webhook" {
		t.Errorf("expected WebhookURL to match")
	}
}
