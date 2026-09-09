package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/fsnotify/fsnotify"
	_ "modernc.org/sqlite"
)

const (
	defaultDBPath1         = "/data/data/com.android.providers.telephony/databases/mmssms.db"
	defaultDBPath2         = "/data/user/0/com.android.providers.telephony/databases/mmssms.db"
	defaultDBPath3         = "/data/user_de/0/com.android.providers.telephony/databases/mmssms.db"
	defaultLastMsgPath     = "./last_msg.txt"
	defaultLastCallPath    = "./last_call.txt"
	defaultCACertDir       = "/system/etc/security/cacerts"
	debounceDelay          = 800 * time.Millisecond
)

type config struct {
	DBPath           string
	LastMsgPath      string
	DNSServer        string
	ProxyURL         string
	TelegramBotToken string
	TelegramChatID   string
	AppriseURL       string
	AppriseURLs      string
	AppriseTitle     string
	AppriseType      string
	AppriseFormat    string
	AppriseUseProxy  bool
	WebhookURL       string
	WebhookMethod    string
	WebhookHeaders   string
	WebhookBody      string
	WebhookUseProxy  bool

	// Bark 配置
	BarkKey      string
	BarkServer   string
	BarkGroup    string
	BarkSound    string
	BarkIcon     string
	BarkUseProxy bool

	// 企业微信群机器人配置
	WechatWebhook  string
	WechatUseProxy bool

	// 通话记录配置
	EnableCallForward bool
	CallDBPath        string
	CallForwardTypes  string
	LastCallPath      string

	CACertFile  string
	CACertDir   string
	TLSInsecure bool
}

type smsRecord struct {
	ID      int64
	Address string
	Body    string
	Type    string    // "sms" (默认) 或 "call"
	Time    time.Time // 事件时间
}

type Sender interface {
	SendSMS(ctx context.Context, record smsRecord) error
}

type multiSender struct {
	senders []Sender
}

func (m *multiSender) SendSMS(ctx context.Context, record smsRecord) error {
	var errs []error
	for _, s := range m.senders {
		if err := s.SendSMS(ctx, record); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type telegramSender struct {
	apiURL string
	chatID string
	client *http.Client
}

func (s *telegramSender) SendSMS(ctx context.Context, record smsRecord) error {
	msgTime := record.Time
	if msgTime.IsZero() {
		msgTime = time.Now()
	}
	timeStr := msgTime.Format("2006-01-02 15:04:05")

	var text string
	if record.Type == "call" {
		text = fmt.Sprintf("📞 【未接来电】\n号码: %s\n时间: %s\n详情: %s", record.Address, timeStr, record.Body)
	} else {
		text = fmt.Sprintf("📩 【收到短信】\n发信人: %s\n时间: %s\n内容: %s", record.Address, timeStr, record.Body)
	}

	payload := map[string]string{
		"chat_id": s.chatID,
		"text":    text,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

type barkSender struct {
	serverURL string
	deviceKey string
	group     string
	sound     string
	icon      string
	client    *http.Client
}

type barkPayload struct {
	DeviceKey string `json:"device_key,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Group     string `json:"group,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Level     string `json:"level,omitempty"`
}

func (s *barkSender) SendSMS(ctx context.Context, record smsRecord) error {
	msgTime := record.Time
	if msgTime.IsZero() {
		msgTime = time.Now()
	}
	timeStr := msgTime.Format("2006-01-02 15:04:05")

	var title, body, group string
	if record.Type == "call" {
		title = fmt.Sprintf("📞 未接来电: %s", record.Address)
		body = fmt.Sprintf("[%s] %s", timeStr, record.Body)
		group = "未接来电"
	} else {
		title = fmt.Sprintf("📩 短信来自: %s", record.Address)
		body = record.Body
		group = s.group
		if group == "" {
			group = "短信转发"
		}
	}

	reqURL := strings.TrimRight(s.serverURL, "/") + "/push"
	payload := barkPayload{
		DeviceKey: s.deviceKey,
		Title:     title,
		Body:      body,
		Group:     group,
		Sound:     s.sound,
		Icon:      s.icon,
		Level:     "active",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bark payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create bark request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post bark message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("bark returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

type wechatSender struct {
	webhookURL string
	client     *http.Client
}

func (s *wechatSender) SendSMS(ctx context.Context, record smsRecord) error {
	msgTime := record.Time
	if msgTime.IsZero() {
		msgTime = time.Now()
	}
	timeStr := msgTime.Format("2006-01-02 15:04:05")

	var content string
	if record.Type == "call" {
		content = fmt.Sprintf("【未接来电通知】\n来电号码：%s\n来电时间：%s\n详情说明：%s", record.Address, timeStr, record.Body)
	} else {
		content = fmt.Sprintf("【收到短信通知】\n发信号码：%s\n收信时间：%s\n短信内容：\n%s", record.Address, timeStr, record.Body)
	}

	payload := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": content,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal wechat payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create wechat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post wechat message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("wechat returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if len(bodyBytes) > 0 && json.Unmarshal(bodyBytes, &result) == nil {
		if result.ErrCode != 0 {
			return fmt.Errorf("wechat api error (code %d): %s", result.ErrCode, result.ErrMsg)
		}
	}

	return nil
}

type appriseSender struct {
	apiURL  string
	urls    string
	title   string
	msgType string
	format  string
	client  *http.Client
}

type apprisePayload struct {
	URLs   string `json:"urls,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body"`
	Type   string `json:"type,omitempty"`
	Format string `json:"format,omitempty"`
}

func (s *appriseSender) SendSMS(ctx context.Context, record smsRecord) error {
	msgTime := record.Time
	if msgTime.IsZero() {
		msgTime = time.Now()
	}
	timeStr := msgTime.Format("2006-01-02 15:04:05")

	var title, body string
	if record.Type == "call" {
		// 严谨处理通话标题：绝不出现“短信”字眼
		if s.title == "" || strings.Contains(s.title, "短信") {
			title = fmt.Sprintf("📞 未接来电: %s", record.Address)
		} else {
			title = strings.ReplaceAll(s.title, "{{from}}", record.Address)
			title = strings.ReplaceAll(title, "{{FROM}}", record.Address)
			title = strings.ReplaceAll(title, "{{type}}", "未接来电")
		}
		body = fmt.Sprintf("【未接来电】\n来电号码: %s\n来电时间: %s\n详情说明: %s", record.Address, timeStr, record.Body)
	} else {
		if s.title == "" {
			title = fmt.Sprintf("📩 收到来自 %s 的短信", record.Address)
		} else {
			title = strings.ReplaceAll(s.title, "{{from}}", record.Address)
			title = strings.ReplaceAll(title, "{{FROM}}", record.Address)
			title = strings.ReplaceAll(title, "{{type}}", "短信")
		}
		body = fmt.Sprintf("【收到短信】\n发信人: %s\n收信时间: %s\n短信内容: %s", record.Address, timeStr, record.Body)
	}

	payload := apprisePayload{
		URLs:   s.urls,
		Title:  title,
		Body:   body,
		Type:   s.msgType,
		Format: s.format,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal apprise payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create apprise request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post apprise message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("apprise returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

type webhookSender struct {
	rawURL       string
	method       string
	headers      string
	bodyTemplate string
	client       *http.Client
}

func (s *webhookSender) SendSMS(ctx context.Context, record smsRecord) error {
	msgTime := record.Time
	if msgTime.IsZero() {
		msgTime = time.Now()
	}
	timeStr := msgTime.Format("2006-01-02 15:04:05")
	timestampMs := strconv.FormatInt(msgTime.UnixMilli(), 10)

	typeVal := "短信"
	typeUpper := "SMS"
	if record.Type == "call" {
		typeVal = "未接来电"
		typeUpper = "CALL"
	}

	reqURL := s.rawURL
	reqURL = strings.ReplaceAll(reqURL, "{{type}}", url.QueryEscape(typeVal))
	reqURL = strings.ReplaceAll(reqURL, "{{TYPE}}", url.QueryEscape(typeUpper))
	reqURL = strings.ReplaceAll(reqURL, "[type]", url.QueryEscape(typeVal))
	reqURL = strings.ReplaceAll(reqURL, "{{from}}", url.QueryEscape(record.Address))
	reqURL = strings.ReplaceAll(reqURL, "{{FROM}}", url.QueryEscape(record.Address))
	reqURL = strings.ReplaceAll(reqURL, "[from]", url.QueryEscape(record.Address))
	reqURL = strings.ReplaceAll(reqURL, "{{body}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{BODY}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{msg}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{MSG}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{content}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{CONTENT}}", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "[content]", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "[msg]", url.QueryEscape(record.Body))
	reqURL = strings.ReplaceAll(reqURL, "{{timestamp}}", timestampMs)
	reqURL = strings.ReplaceAll(reqURL, "{{time}}", url.QueryEscape(timeStr))

	headerMap := parseHeaders(s.headers)

	var bodyReader io.Reader
	tmpl := strings.TrimSpace(s.bodyTemplate)

	if tmpl == "" {
		if s.method != http.MethodGet {
			defaultPayload := map[string]any{
				"type":      typeVal,
				"from":      record.Address,
				"content":   record.Body,
				"time":      timeStr,
				"timestamp": msgTime.UnixMilli(),
			}
			data, err := json.Marshal(defaultPayload)
			if err != nil {
				return fmt.Errorf("marshal default webhook payload: %w", err)
			}
			bodyReader = bytes.NewReader(data)
			if headerMap.Get("Content-Type") == "" {
				headerMap.Set("Content-Type", "application/json; charset=utf-8")
			}
		}
	} else {
		isJSON := strings.HasPrefix(tmpl, "{") || strings.HasPrefix(tmpl, "[")
		addressVal := record.Address
		bodyVal := record.Body
		if isJSON {
			addressVal = jsonEscape(addressVal)
			bodyVal = jsonEscape(bodyVal)
		}

		bodyStr := tmpl
		bodyStr = strings.ReplaceAll(bodyStr, "{{type}}", typeVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{TYPE}}", typeUpper)
		bodyStr = strings.ReplaceAll(bodyStr, "[type]", typeVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{from}}", addressVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{FROM}}", addressVal)
		bodyStr = strings.ReplaceAll(bodyStr, "[from]", addressVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{body}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{BODY}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{msg}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{MSG}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{content}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{CONTENT}}", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "[content]", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "[msg]", bodyVal)
		bodyStr = strings.ReplaceAll(bodyStr, "{{timestamp}}", timestampMs)
		bodyStr = strings.ReplaceAll(bodyStr, "{{time}}", timeStr)

		bodyReader = strings.NewReader(bodyStr)

		if headerMap.Get("Content-Type") == "" {
			if isJSON {
				headerMap.Set("Content-Type", "application/json; charset=utf-8")
			} else {
				headerMap.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, s.method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}

	for k, vv := range headerMap {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webhook returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

func parseHeaders(headerStr string) http.Header {
	h := make(http.Header)
	scanner := bufio.NewScanner(strings.NewReader(headerStr))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			h.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
	return h
}

func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return string(b[1 : len(b)-1])
	}
	return s
}

func loadEnvFile(path string) map[string]string {
	result := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			result[key] = val
		}
	}
	return result
}

func setupAndroidDNS(customDNS string) {
	var dnsList []string
	if customDNS != "" {
		if !strings.Contains(customDNS, ":") {
			customDNS += ":53"
		}
		dnsList = append(dnsList, customDNS)
	}

	for _, prop := range []string{"net.dns1", "net.dns2", "net.wlan0.dns1", "net.rmnet_data0.dns1"} {
		out, err := exec.Command("getprop", prop).Output()
		if err == nil {
			val := strings.TrimSpace(string(out))
			if val != "" && !strings.HasPrefix(val, "::1") && !strings.HasPrefix(val, "127.0.0.1") {
				if !strings.Contains(val, ":") {
					val += ":53"
				}
				dnsList = append(dnsList, val)
			}
		}
	}

	dnsList = append(dnsList, "223.5.5.5:53", "119.29.29.29:53", "114.114.114.114:53", "8.8.8.8:53", "1.1.1.1:53")
	log.Printf("[INIT] Configured DNS fallback resolvers: %s", dnsList[0])

	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 3 * time.Second,
			}
			var lastErr error
			for _, server := range dnsList {
				conn, err := d.DialContext(ctx, "udp", server)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, fmt.Errorf("all DNS resolvers failed: %w", lastErr)
		},
	}
}

func openSQLiteDB(path string) (*sql.DB, error) {
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		canonicalPath = path
	}
	dsn := fmt.Sprintf("file:%s?mode=ro", canonicalPath)
	return sql.Open("sqlite", dsn)
}

func openSQLiteDBWithRetry(ctx context.Context, path string) (*sql.DB, error) {
	var db *sql.DB
	var err error
	for attempt := 1; attempt <= 30; attempt++ {
		db, err = openSQLiteDB(path)
		if err == nil {
			err = db.PingContext(ctx)
			if err == nil {
				return db, nil
			}
			db.Close()
		}
		log.Printf("[WAIT] Waiting for SQLite database %s to become available (%v)... attempt %d/30", filepath.Base(path), err, attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("open sqlite database %s after retries: %w", path, err)
}

func scanDatabases() {
	log.Printf("[SCAN] Scanning Android directories for SMS/MMS and Call databases...")
	roots := []string{
		"/data/data",
		"/data/user/0",
		"/data/user_de/0",
	}

	packages := []string{
		"telephony",
		"mms",
		"sms",
		"message",
		"messaging",
		"contacts",
		"dialer",
		"calllog",
	}

	foundCount := 0
	for _, root := range roots {
		for _, pkg := range packages {
			pattern := filepath.Join(root, "*"+pkg+"*", "databases", "*.db")
			matches, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}
			for _, dbFile := range matches {
				foundCount++
				db, err := openSQLiteDB(dbFile)
				if err != nil {
					log.Printf("[SCAN] Found: %s (open error: %v)", dbFile, err)
					continue
				}

				rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' OR type='view'")
				if err != nil {
					db.Close()
					continue
				}
				var tables []string
				for rows.Next() {
					var t string
					if err := rows.Scan(&t); err == nil {
						tables = append(tables, t)
					}
				}
				rows.Close()

				log.Printf("[SCAN] Database: %s -> Tables: %v", dbFile, tables)
				db.Close()
			}
		}
	}
	if foundCount == 0 {
		log.Printf("[SCAN] No matching database files found in standard locations.")
	}
}

func findActiveDatabase(explicitPath string) (string, int64, error) {
	if explicitPath != "" {
		if db, err := openSQLiteDB(explicitPath); err == nil {
			var count int64
			_ = db.QueryRow("SELECT count(*) FROM sms").Scan(&count)
			db.Close()
			if count > 0 {
				return explicitPath, count, nil
			}
			log.Printf("[INIT] Explicit DB_PATH %s has 0 messages. Scanning for active database...", explicitPath)
		}
	}

	roots := []string{
		"/data/data",
		"/data/user/0",
		"/data/user_de/0",
	}
	packages := []string{
		"telephony",
		"mms",
		"sms",
		"messaging",
	}

	var bestPath string
	var maxCount int64 = -1

	for _, root := range roots {
		for _, pkg := range packages {
			pattern := filepath.Join(root, "*"+pkg+"*", "databases", "*.db")
			matches, _ := filepath.Glob(pattern)
			for _, dbFile := range matches {
				db, err := openSQLiteDB(dbFile)
				if err != nil {
					continue
				}
				var count int64
				err = db.QueryRow("SELECT count(*) FROM sms").Scan(&count)
				db.Close()
				if err == nil && count > 0 {
					log.Printf("[INIT] Detected candidate SMS DB: %s (contains %d messages in 'sms' table)", dbFile, count)
					if count > maxCount {
						maxCount = count
						bestPath = dbFile
					}
				}
			}
		}
	}

	if bestPath != "" && maxCount > 0 {
		return bestPath, maxCount, nil
	}

	if explicitPath != "" {
		return explicitPath, 0, nil
	}

	return "/data/data/com.android.providers.telephony/databases/mmssms.db", 0, nil
}

func findActiveCallDatabase(explicitPath string) (string, int64, error) {
	if explicitPath != "" {
		if db, err := openSQLiteDB(explicitPath); err == nil {
			var count int64
			err = db.QueryRow("SELECT count(*) FROM calls").Scan(&count)
			db.Close()
			if err == nil {
				return explicitPath, count, nil
			}
			log.Printf("[CALL INIT] Explicit CALL_DB_PATH %s has no 'calls' table: %v", explicitPath, err)
		}
	}

	candidates := []string{
		"/data/data/com.android.providers.contacts/databases/calllog.db",
		"/data/user/0/com.android.providers.contacts/databases/calllog.db",
		"/data/user_de/0/com.android.providers.contacts/databases/calllog.db",
		"/data/data/com.android.providers.contacts/databases/contacts2.db",
		"/data/user/0/com.android.providers.contacts/databases/contacts2.db",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if db, err := openSQLiteDB(path); err == nil {
			var count int64
			err = db.QueryRow("SELECT count(*) FROM calls").Scan(&count)
			db.Close()
			if err == nil {
				log.Printf("[CALL INIT] Found active call database: %s (total records in 'calls': %d)", path, count)
				return path, count, nil
			}
		}
	}

	roots := []string{
		"/data/data",
		"/data/user/0",
		"/data/user_de/0",
	}
	packages := []string{
		"contacts",
		"dialer",
		"calllog",
		"phone",
	}

	for _, root := range roots {
		for _, pkg := range packages {
			pattern := filepath.Join(root, "*"+pkg+"*", "databases", "*.db")
			matches, _ := filepath.Glob(pattern)
			for _, dbFile := range matches {
				if db, err := openSQLiteDB(dbFile); err == nil {
					var count int64
					err = db.QueryRow("SELECT count(*) FROM calls").Scan(&count)
					db.Close()
					if err == nil {
						log.Printf("[CALL INIT] Found candidate call database: %s (contains %d calls)", dbFile, count)
						return dbFile, count, nil
					}
				}
			}
		}
	}

	return "", 0, errors.New("call database not found")
}

func initTimezone() {
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		time.Local = loc
	}
}

// In-memory message deduplication ring buffer (holds hash of last 50 sent messages/calls)
var sentHashes = make(map[string]bool)
var sentQueue []string

func isDuplicate(address, body string) bool {
	sum := md5.Sum([]byte(address + "::" + body))
	h := hex.EncodeToString(sum[:])
	if sentHashes[h] {
		return true
	}
	sentHashes[h] = true
	sentQueue = append(sentQueue, h)
	if len(sentQueue) > 50 {
		oldest := sentQueue[0]
		sentQueue = sentQueue[1:]
		delete(sentHashes, oldest)
	}
	return false
}

func parseAllowedCallTypes(s string) []int {
	if strings.TrimSpace(s) == "" {
		return []int{3, 5}
	}
	var res []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if val, err := strconv.Atoi(part); err == nil {
			res = append(res, val)
		}
	}
	if len(res) == 0 {
		return []int{3, 5}
	}
	return res
}

func main() {
	initTimezone()

	var configPath string
	var testMode bool
	var scanMode bool
	flag.StringVar(&configPath, "config", "", "path to config.env")
	flag.BoolVar(&testMode, "test", false, "send the latest SMS and Call as a test")
	flag.BoolVar(&scanMode, "scan", false, "scan Android directories for all SMS and Call databases")
	flag.Parse()

	if scanMode {
		scanDatabases()
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, configPath, testMode); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("[FATAL] %v", err)
	}
}

func run(ctx context.Context, configPath string, testMode bool) error {
	cfg, actualConfigPath, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	setupAndroidDNS(cfg.DNSServer)

	sender, err := newSenders(cfg)
	if err != nil {
		return err
	}

	// 1. 初始化短信数据库
	realDBPath, smsCount, err := findActiveDatabase(cfg.DBPath)
	if err != nil {
		return err
	}
	cfg.DBPath = realDBPath
	log.Printf("[INIT] Using SMS database: %s (total messages: %d)", cfg.DBPath, smsCount)

	smsDB, err := openSQLiteDBWithRetry(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open sms database %s: %w", cfg.DBPath, err)
	}
	defer smsDB.Close()

	// 2. 初始化通话记录数据库（如果启用）
	var callDB *sql.DB
	var lastCallID int64
	var canonicalCallDBPath string
	var callAllowedTypes []int

	if cfg.EnableCallForward {
		callAllowedTypes = parseAllowedCallTypes(cfg.CallForwardTypes)
		realCallDBPath, callCount, err := findActiveCallDatabase(cfg.CallDBPath)
		if err == nil {
			cfg.CallDBPath = realCallDBPath
			log.Printf("[INIT] Using Call database: %s (total records in 'calls': %d)", cfg.CallDBPath, callCount)
			cDB, err := openSQLiteDBWithRetry(ctx, cfg.CallDBPath)
			if err == nil {
				callDB = cDB
				defer callDB.Close()
				lastCallID, err = loadOrInitializeLastCallID(ctx, callDB, cfg.LastCallPath)
				if err == nil {
					log.Printf("[INIT] Starting Calls from last processed _id=%d (previous calls will not be resent)", lastCallID)
					if abs, err := filepath.Abs(cfg.CallDBPath); err == nil {
						if cPath, err := filepath.EvalSymlinks(abs); err == nil {
							canonicalCallDBPath = cPath
						} else {
							canonicalCallDBPath = abs
						}
					}
				} else {
					log.Printf("[CALL WARN] Failed to load or initialize last call id: %v", err)
				}
			} else {
				log.Printf("[CALL WARN] Failed to open call database %s: %v", cfg.CallDBPath, err)
			}
		} else {
			log.Printf("[CALL INFO] Call database not found or access restricted; continuing with SMS forwarding only.")
		}
	}

	// 测试模式
	if testMode {
		log.Printf("[TEST] Querying latest SMS to test forwarding...")
		var testRecord smsRecord
		smsQuery := "SELECT _id, address, body FROM sms ORDER BY _id DESC LIMIT 1"
		scanErr := smsDB.QueryRowContext(ctx, smsQuery).Scan(&testRecord.ID, &testRecord.Address, &testRecord.Body)
		if scanErr != nil {
			log.Printf("[TEST] No SMS found in database, sending simulated test message...")
			testRecord = smsRecord{
				ID:      9999,
				Address: "TEST_BOT",
				Body:    "这是一条来自 SMS Forwarder 的测试短信通知！配置正确！",
				Type:    "sms",
				Time:    time.Now(),
			}
		} else {
			testRecord.Type = "sms"
			testRecord.Time = time.Now()
		}
		log.Printf("[TEST] Sending SMS (_id=%d from %s): %s", testRecord.ID, testRecord.Address, testRecord.Body)
		if err := sender.SendSMS(ctx, testRecord); err != nil {
			return fmt.Errorf("test send sms failed: %w", err)
		}

		if callDB != nil {
			log.Printf("[TEST] Querying latest Call to test forwarding...")
			var testCall smsRecord
			var dateMs int64
			var duration, ctype int
			callQuery := "SELECT _id, number, date, duration, type FROM calls ORDER BY _id DESC LIMIT 1"
			cScanErr := callDB.QueryRowContext(ctx, callQuery).Scan(&testCall.ID, &testCall.Address, &dateMs, &duration, &ctype)

			if cScanErr == nil {
				testCall.Type = "call"
				testCall.Time = time.UnixMilli(dateMs)
				testCall.Body = fmt.Sprintf("未接来电 (测试响铃 %d 秒)", duration)
				log.Printf("[TEST] Sending Call (_id=%d from %s): %s", testCall.ID, testCall.Address, testCall.Body)
				_ = sender.SendSMS(ctx, testCall)
			}
		}

		log.Printf("[TEST SUCCESS] Test message(s) sent successfully!")
		return nil
	}

	lastID, err := loadOrInitializeLastID(ctx, smsDB, cfg.LastMsgPath)
	if err != nil {
		return err
	}
	log.Printf("[INIT] Starting SMS from last processed _id=%d (previous SMS will not be resent, waiting for new SMS)", lastID)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	defer watcher.Close()

	// 监控 SMS 目录和文件 (不监控 -journal 文件，防止 SQLite 读锁递归触发)
	smsAbsPath, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	canonicalSMSPath, err := filepath.EvalSymlinks(smsAbsPath)
	if err != nil {
		canonicalSMSPath = smsAbsPath
	}
	smsWalPath := canonicalSMSPath + "-wal"
	smsWatchDir := filepath.Dir(canonicalSMSPath)

	watched := map[string]bool{}
	if err := addWatch(watcher, watched, smsWatchDir); err != nil {
		return err
	}
	_ = addWatchIfExists(watcher, watched, canonicalSMSPath)
	_ = addWatchIfExists(watcher, watched, smsWalPath)

	// 监控 Call 目录和文件（如果开启）
	if canonicalCallDBPath != "" {
		callWatchDir := filepath.Dir(canonicalCallDBPath)
		_ = addWatch(watcher, watched, callWatchDir)
		_ = addWatchIfExists(watcher, watched, canonicalCallDBPath)
		_ = addWatchIfExists(watcher, watched, canonicalCallDBPath+"-wal")
		log.Printf("[READY] Watching Call directory: %s", callWatchDir)
	}

	if actualConfigPath != "" {
		configDir := filepath.Dir(actualConfigPath)
		if err := addWatch(watcher, watched, configDir); err != nil {
			log.Printf("[WARN] Failed to watch config directory %s: %v", configDir, err)
		} else {
			log.Printf("[READY] Hot-reload enabled for config: %s", actualConfigPath)
		}
	}

	testTriggerPath := "/data/adb/sms_tg_forwarder/test"
	testCallTriggerPath := "/data/adb/sms_tg_forwarder/test_call"
	_ = os.Remove(testTriggerPath)
	_ = os.Remove(testCallTriggerPath)

	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)

	processSMS := func() {
		nextLastID, err := processNewMessages(ctx, smsDB, sender, cfg.LastMsgPath, lastID)
		if err != nil {
			log.Printf("[ERROR] Process new SMS: %v", err)
			return
		}
		lastID = nextLastID
	}

	processCall := func() {
		if callDB == nil {
			return
		}
		nextLastCallID, err := processNewCalls(ctx, callDB, sender, cfg.LastCallPath, lastCallID, callAllowedTypes)
		if err != nil {
			log.Printf("[ERROR] Process new calls: %v", err)
			return
		}
		lastCallID = nextLastCallID
	}

	smsDebounce := time.NewTimer(time.Hour)
	if !smsDebounce.Stop() {
		<-smsDebounce.C
	}

	callDebounce := time.NewTimer(time.Hour)
	if !callDebounce.Stop() {
		<-callDebounce.C
	}

	configDebounce := time.NewTimer(time.Hour)
	if !configDebounce.Stop() {
		<-configDebounce.C
	}

	doReload := func() {
		log.Printf("[HOT-RELOAD] Reloading configuration from %s...", actualConfigPath)
		newCfg, _, err := loadConfig(actualConfigPath)
		if err != nil {
			log.Printf("[HOT-RELOAD ERROR] Failed to parse config: %v (keeping current config)", err)
			return
		}
		newSender, err := newSenders(newCfg)
		if err != nil {
			log.Printf("[HOT-RELOAD ERROR] Failed to initialize senders: %v (keeping current config)", err)
			return
		}
		cfg = newCfg
		sender = newSender
		if cfg.EnableCallForward {
			callAllowedTypes = parseAllowedCallTypes(cfg.CallForwardTypes)
		}
		log.Printf("[HOT-RELOAD SUCCESS] Configuration reloaded and applied successfully!")
	}

	log.Printf("[READY] Watching SMS directory: %s", smsWatchDir)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hupChan:
			log.Printf("[SIGNAL] Received SIGHUP, reloading configuration...")
			doReload()
		case <-configDebounce.C:
			doReload()
		case <-smsDebounce.C:
			processSMS()
		case <-callDebounce.C:
			processCall()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// 测试触发判断 (短信)
			if _, err := os.Stat(testTriggerPath); err == nil {
				_ = os.Remove(testTriggerPath)
				log.Printf("[TEST TRIGGER] Found %s, sending latest SMS...", testTriggerPath)
				var testRecord smsRecord
				q := "SELECT _id, address, body FROM sms ORDER BY _id DESC LIMIT 1"
				if qErr := smsDB.QueryRowContext(ctx, q).Scan(&testRecord.ID, &testRecord.Address, &testRecord.Body); qErr == nil {
					testRecord.Type = "sms"
					testRecord.Time = time.Now()
					_ = sender.SendSMS(ctx, testRecord)
				}
				continue
			}

			// 测试触发判断 (来电)
			if _, err := os.Stat(testCallTriggerPath); err == nil {
				_ = os.Remove(testCallTriggerPath)
				log.Printf("[TEST TRIGGER] Found %s, sending simulated call...", testCallTriggerPath)
				testCall := smsRecord{
					ID:      8888,
					Address: "13800138000",
					Body:    "未接来电 (响铃 18 秒)",
					Type:    "call",
					Time:    time.Now(),
				}
				if callDB != nil {
					var dateMs int64
					var duration, ctype int
					cq := "SELECT _id, number, date, duration, type FROM calls ORDER BY _id DESC LIMIT 1"
					if cqErr := callDB.QueryRowContext(ctx, cq).Scan(&testCall.ID, &testCall.Address, &dateMs, &duration, &ctype); cqErr == nil {
						testCall.Time = time.UnixMilli(dateMs)
						testCall.Body = fmt.Sprintf("未接来电 (响铃 %d 秒)", duration)
					}
				}
				_ = sender.SendSMS(ctx, testCall)
				continue
			}

			// 配置文件变动
			if actualConfigPath != "" {
				cleanEventName := filepath.Clean(event.Name)
				if cleanEventName == actualConfigPath || cleanEventName == filepath.Base(actualConfigPath) {
					if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
						log.Printf("[CONFIG EVENT] Detected modification on %s, scheduling reload...", actualConfigPath)
						resetTimer(configDebounce, 500*time.Millisecond)
						continue
					}
				}
			}

			// 检查是否属于 SMS 数据库变动
			if isRelevantEvent(event, canonicalSMSPath) {
				if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
					markUnwatched(watcher, watched, event.Name)
				}
				_ = addWatchIfExists(watcher, watched, canonicalSMSPath)
				_ = addWatchIfExists(watcher, watched, smsWalPath)
				resetTimer(smsDebounce, debounceDelay)
				continue
			}

			// 检查是否属于 Call 数据库变动
			if canonicalCallDBPath != "" && isRelevantEvent(event, canonicalCallDBPath) {
				if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
					markUnwatched(watcher, watched, event.Name)
				}
				_ = addWatchIfExists(watcher, watched, canonicalCallDBPath)
				_ = addWatchIfExists(watcher, watched, canonicalCallDBPath+"-wal")
				resetTimer(callDebounce, debounceDelay)
				continue
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("[ERROR] Watcher error: %v", err)
		}
	}
}

func loadConfig(configPath string) (config, string, error) {
	envMap := make(map[string]string)
	candidates := []string{
		configPath,
		os.Getenv("CONFIG_FILE"),
		"/data/adb/modules/sms_tg_forwarder/config.env",
		"./config.env",
	}

	actualConfigPath := ""
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			actualConfigPath, _ = filepath.Abs(path)
			envMap = loadEnvFile(actualConfigPath)
			break
		}
	}

	get := func(key, fallback string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		if v, ok := envMap[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return fallback
	}

	// 兼容支持 LAST_MSG_PATH 与旧的 LAST_ID_PATH
	lastMsgPath := get("LAST_MSG_PATH", "")
	if lastMsgPath == "" {
		lastMsgPath = get("LAST_ID_PATH", defaultLastMsgPath)
	}

	// 兼容支持 LAST_CALL_PATH 与旧的 LAST_CALL_ID_PATH
	lastCallPath := get("LAST_CALL_PATH", "")
	if lastCallPath == "" {
		lastCallPath = get("LAST_CALL_ID_PATH", defaultLastCallPath)
	}

	cfg := config{
		DBPath:           get("DB_PATH", ""),
		LastMsgPath:      lastMsgPath,
		DNSServer:        get("DNS_SERVER", ""),
		ProxyURL:         get("PROXY_URL", ""),
		TelegramBotToken: get("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   get("TELEGRAM_CHAT_ID", ""),
		AppriseURL:       get("APPRISE_URL", ""),
		AppriseURLs:      get("APPRISE_URLS", ""),
		AppriseTitle:     get("APPRISE_TITLE", ""),
		AppriseType:      get("APPRISE_TYPE", "info"),
		AppriseFormat:    get("APPRISE_FORMAT", "text"),
		WebhookURL:       get("WEBHOOK_URL", ""),
		WebhookMethod:    get("WEBHOOK_METHOD", "POST"),
		WebhookHeaders:   get("WEBHOOK_HEADERS", ""),
		WebhookBody:      get("WEBHOOK_BODY", ""),
		CACertFile:       get("CA_CERT_FILE", ""),
		CACertDir:        get("CA_CERT_DIR", defaultCACertDir),

		// Bark
		BarkKey:    get("BARK_KEY", ""),
		BarkServer: get("BARK_SERVER", "https://api.day.app"),
		BarkGroup:  get("BARK_GROUP", "短信转发"),
		BarkSound:  get("BARK_SOUND", ""),
		BarkIcon:   get("BARK_ICON", ""),

		// 企业微信群机器人
		WechatWebhook: get("WECHAT_WEBHOOK", get("WX_WEBHOOK", "")),

		// 通话记录
		CallDBPath:       get("CALL_DB_PATH", ""),
		CallForwardTypes: get("CALL_FORWARD_TYPES", "3,5"),
		LastCallPath:     lastCallPath,
	}

	enableCallStr := get("ENABLE_CALL_FORWARD", "true")
	cfg.EnableCallForward, _ = strconv.ParseBool(enableCallStr)

	barkUseProxy, _ := strconv.ParseBool(get("BARK_USE_PROXY", "false"))
	cfg.BarkUseProxy = barkUseProxy

	wechatUseProxy, _ := strconv.ParseBool(get("WECHAT_USE_PROXY", "false"))
	cfg.WechatUseProxy = wechatUseProxy

	tlsInsecure, _ := strconv.ParseBool(get("TLS_INSECURE_SKIP_VERIFY", "false"))
	cfg.TLSInsecure = tlsInsecure

	appriseUseProxy, _ := strconv.ParseBool(get("APPRISE_USE_PROXY", "false"))
	cfg.AppriseUseProxy = appriseUseProxy

	webhookUseProxy, _ := strconv.ParseBool(get("WEBHOOK_USE_PROXY", "false"))
	cfg.WebhookUseProxy = webhookUseProxy

	hasTelegram := cfg.TelegramBotToken != "" && cfg.TelegramChatID != ""
	hasApprise := cfg.AppriseURL != ""
	hasWebhook := cfg.WebhookURL != ""
	hasBark := cfg.BarkKey != ""
	hasWechat := cfg.WechatWebhook != ""

	if !hasTelegram && !hasApprise && !hasWebhook && !hasBark && !hasWechat {
		return config{}, actualConfigPath, errors.New("at least one destination must be configured: set BARK_KEY, WECHAT_WEBHOOK, APPRISE_URL, WEBHOOK_URL, or (TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID)")
	}

	return cfg, actualConfigPath, nil
}

func loadOrInitializeLastID(ctx context.Context, db *sql.DB, path string) (int64, error) {
	var maxID int64
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(_id), 0) FROM sms`).Scan(&maxID)

	// 优先读取目标路径，如不存在则尝试读取旧格式兼容文件 ./last_id.txt
	checkPaths := []string{path}
	if filepath.Base(path) == "last_msg.txt" {
		checkPaths = append(checkPaths, filepath.Join(filepath.Dir(path), "last_id.txt"))
	}

	for _, p := range checkPaths {
		data, err := os.ReadFile(p)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			lastLine := strings.TrimSpace(lines[len(lines)-1])
			if lastID, err := strconv.ParseInt(lastLine, 10, 64); err == nil {
				if lastID > maxID {
					_ = writeLastID(path, maxID)
					return maxID, nil
				}
				_ = writeLastID(path, lastID)
				return lastID, nil
			}
		}
	}

	_ = writeLastID(path, maxID)
	return maxID, nil
}

func loadOrInitializeLastCallID(ctx context.Context, db *sql.DB, path string) (int64, error) {
	var maxID int64
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(_id), 0) FROM calls`).Scan(&maxID)

	checkPaths := []string{path}
	if filepath.Base(path) == "last_call.txt" {
		checkPaths = append(checkPaths, filepath.Join(filepath.Dir(path), "last_call_id.txt"))
	}

	for _, p := range checkPaths {
		data, err := os.ReadFile(p)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			lastLine := strings.TrimSpace(lines[len(lines)-1])
			if lastID, err := strconv.ParseInt(lastLine, 10, 64); err == nil {
				if lastID > maxID {
					_ = writeLastID(path, maxID)
					return maxID, nil
				}
				_ = writeLastID(path, lastID)
				return lastID, nil
			}
		}
	}

	_ = writeLastID(path, maxID)
	return maxID, nil
}

func processNewMessages(ctx context.Context, db *sql.DB, sender Sender, lastMsgPath string, lastID int64) (int64, error) {
	var maxIDInDB int64
	_ = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(_id), 0) FROM sms").Scan(&maxIDInDB)

	if lastID > maxIDInDB {
		log.Printf("[WARN] Stored last_msg (%d) > current max_id in DB (%d). Adjusting to %d", lastID, maxIDInDB, maxIDInDB)
		_ = writeLastID(lastMsgPath, maxIDInDB)
		return maxIDInDB, nil
	}

	query := "SELECT _id, type, address, body FROM sms WHERE _id > ? ORDER BY _id ASC"
	rows, err := db.QueryContext(ctx, query, lastID)
	if err != nil {
		return lastID, fmt.Errorf("query sms records after _id=%d: %w", lastID, err)
	}
	defer rows.Close()

	currentLastID := lastID

	for rows.Next() {
		var id int64
		var msgType int
		var address, body string

		if err := rows.Scan(&id, &msgType, &address, &body); err != nil {
			log.Printf("[WARN] Scan sms row error: %v", err)
			continue
		}

		// msgType == 2 is user outgoing sent SMS
		if msgType == 2 {
			log.Printf("[SKIP] Skipping outgoing/sent SMS (_id=%d, type=2)", id)
			currentLastID = id
			_ = writeLastID(lastMsgPath, id)
			continue
		}

		if isDuplicate(address, body) {
			log.Printf("[DEDUP] Skipping already-sent identical message (_id=%d from %s)", id, address)
			currentLastID = id
			_ = writeLastID(lastMsgPath, id)
			continue
		}

		record := smsRecord{
			ID:      id,
			Address: address,
			Body:    body,
			Type:    "sms",
			Time:    time.Now(),
		}

		log.Printf("[SENDING] Dispatching SMS _id=%d from %s to notification channels...", id, address)
		if err := sender.SendSMS(ctx, record); err != nil {
			log.Printf("[WARN] Failed to send SMS _id=%d: %v", id, err)
			_ = writeLastID(lastMsgPath, id)
			currentLastID = id
			return currentLastID, fmt.Errorf("send sms _id=%d: %w", id, err)
		}

		_ = writeLastID(lastMsgPath, id)
		currentLastID = id
		log.Printf("[FORWARD SUCCESS] SMS _id=%d from %s forwarded successfully!", id, address)
	}
	if err := rows.Err(); err != nil {
		return currentLastID, fmt.Errorf("iterate sms records: %w", err)
	}

	return currentLastID, nil
}

func processNewCalls(ctx context.Context, db *sql.DB, sender Sender, lastCallPath string, lastCallID int64, allowedTypes []int) (int64, error) {
	var maxIDInDB int64
	_ = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(_id), 0) FROM calls").Scan(&maxIDInDB)

	if lastCallID > maxIDInDB {
		log.Printf("[CALL WARN] Stored last_call (%d) > current max_id in DB (%d). Adjusting to %d", lastCallID, maxIDInDB, maxIDInDB)
		_ = writeLastID(lastCallPath, maxIDInDB)
		return maxIDInDB, nil
	}

	// 动态检测 calls 表字段是否存在 name 字段
	hasNameCol := false
	rowsCol, err := db.QueryContext(ctx, "PRAGMA table_info(calls)")
	if err == nil {
		for rowsCol.Next() {
			var cid int
			var cname, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := rowsCol.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err == nil {
				if cname == "name" {
					hasNameCol = true
					break
				}
			}
		}
		rowsCol.Close()
	}

	nameSelect := "'' AS name"
	if hasNameCol {
		nameSelect = "COALESCE(name, '') AS name"
	}

	query := fmt.Sprintf("SELECT _id, number, date, duration, type, %s FROM calls WHERE _id > ? ORDER BY _id ASC", nameSelect)
	rows, err := db.QueryContext(ctx, query, lastCallID)
	if err != nil {
		return lastCallID, fmt.Errorf("query calls after _id=%d: %w", lastCallID, err)
	}
	defer rows.Close()

	typeMap := make(map[int]bool)
	for _, t := range allowedTypes {
		typeMap[t] = true
	}

	currentLastID := lastCallID

	for rows.Next() {
		var id int64
		var number string
		var dateMs int64
		var duration int
		var callType int
		var callerName string

		if err := rows.Scan(&id, &number, &dateMs, &duration, &callType, &callerName); err != nil {
			log.Printf("[CALL WARN] Scan call row error: %v", err)
			continue
		}

		if !typeMap[callType] {
			currentLastID = id
			_ = writeLastID(lastCallPath, id)
			continue
		}

		callTime := time.UnixMilli(dateMs)
		if dateMs <= 0 {
			callTime = time.Now()
		}

		callerIdentity := number
		if callerName != "" && callerName != number {
			callerIdentity = fmt.Sprintf("%s (%s)", callerName, number)
		}

		var detail string
		switch callType {
		case 3:
			if duration > 0 {
				detail = fmt.Sprintf("未接来电 (响铃 %d 秒)", duration)
			} else {
				detail = "未接来电"
			}
		case 5:
			detail = "拒接来电"
		case 1:
			detail = fmt.Sprintf("已接来电 (通话 %d 秒)", duration)
		case 6:
			detail = "拦截来电"
		default:
			detail = fmt.Sprintf("电话来电 (代码 %d)", callType)
		}

		if isDuplicate(callerIdentity, detail+"::"+strconv.FormatInt(dateMs, 10)) {
			log.Printf("[CALL DEDUP] Skipping duplicate call (_id=%d from %s)", id, callerIdentity)
			currentLastID = id
			_ = writeLastID(lastCallPath, id)
			continue
		}

		record := smsRecord{
			ID:      id,
			Address: callerIdentity,
			Body:    detail,
			Type:    "call",
			Time:    callTime,
		}

		log.Printf("[CALL SENDING] Dispatching call notification _id=%d from %s...", id, callerIdentity)
		if err := sender.SendSMS(ctx, record); err != nil {
			log.Printf("[CALL WARN] Failed to send call notification _id=%d: %v", id, err)
			_ = writeLastID(lastCallPath, id)
			currentLastID = id
			return currentLastID, fmt.Errorf("send call notification _id=%d: %w", id, err)
		}

		_ = writeLastID(lastCallPath, id)
		currentLastID = id
		log.Printf("[CALL FORWARD SUCCESS] Call notification _id=%d from %s forwarded successfully!", id, callerIdentity)
	}

	if err := rows.Err(); err != nil {
		return currentLastID, fmt.Errorf("iterate call records: %w", err)
	}

	return currentLastID, nil
}

func writeLastID(path string, id int64) error {
	tmpPath := path + ".tmp"
	data := []byte(strconv.FormatInt(id, 10) + "\n")

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temporary last id file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace last id file: %w", err)
	}
	return nil
}

func newSenders(cfg config) (Sender, error) {
	var senders []Sender

	tlsConfig, err := newTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	var proxyTransport *http.Transport
	if cfg.ProxyURL != "" {
		proxyURL, err := url.Parse(cfg.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url: %w", err)
		}
		proxyTransport = &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: tlsConfig,
		}
	}

	directTransport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsConfig,
	}

	// 1. Telegram
	if cfg.TelegramBotToken != "" && cfg.TelegramChatID != "" {
		transport := directTransport
		if proxyTransport != nil {
			transport = proxyTransport
		}
		senders = append(senders, &telegramSender{
			apiURL: fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramBotToken),
			chatID: cfg.TelegramChatID,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	// 2. Bark
	if cfg.BarkKey != "" {
		transport := directTransport
		if cfg.BarkUseProxy && proxyTransport != nil {
			transport = proxyTransport
		}
		server := cfg.BarkServer
		if server == "" {
			server = "https://api.day.app"
		}
		senders = append(senders, &barkSender{
			serverURL: server,
			deviceKey: cfg.BarkKey,
			group:     cfg.BarkGroup,
			sound:     cfg.BarkSound,
			icon:      cfg.BarkIcon,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	// 3. 企业微信群机器人
	if cfg.WechatWebhook != "" {
		transport := directTransport
		if cfg.WechatUseProxy && proxyTransport != nil {
			transport = proxyTransport
		}
		senders = append(senders, &wechatSender{
			webhookURL: cfg.WechatWebhook,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	// 4. Apprise
	if cfg.AppriseURL != "" {
		transport := directTransport
		if cfg.AppriseUseProxy && proxyTransport != nil {
			transport = proxyTransport
		}
		senders = append(senders, &appriseSender{
			apiURL:  cfg.AppriseURL,
			urls:    cfg.AppriseURLs,
			title:   cfg.AppriseTitle,
			msgType: cfg.AppriseType,
			format:  cfg.AppriseFormat,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	// 5. 通用 Webhook
	if cfg.WebhookURL != "" {
		transport := directTransport
		if cfg.WebhookUseProxy && proxyTransport != nil {
			transport = proxyTransport
		}
		senders = append(senders, &webhookSender{
			rawURL:       cfg.WebhookURL,
			method:       cfg.WebhookMethod,
			headers:      cfg.WebhookHeaders,
			bodyTemplate: cfg.WebhookBody,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	if len(senders) == 0 {
		return nil, errors.New("no valid senders configured")
	}

	return &multiSender{senders: senders}, nil
}

func newTLSConfig(cfg config) (*tls.Config, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	if cfg.CACertFile != "" {
		certBytes, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read ca cert file %s: %w", cfg.CACertFile, err)
		}
		if !rootCAs.AppendCertsFromPEM(certBytes) {
			return nil, fmt.Errorf("append ca cert %s: invalid cert format", cfg.CACertFile)
		}
	}

	if cfg.CACertDir != "" {
		entries, err := os.ReadDir(cfg.CACertDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				certBytes, err := os.ReadFile(filepath.Join(cfg.CACertDir, entry.Name()))
				if err == nil {
					rootCAs.AppendCertsFromPEM(certBytes)
				}
			}
		}
	}

	return &tls.Config{
		RootCAs:            rootCAs,
		InsecureSkipVerify: cfg.TLSInsecure,
	}, nil
}

func addWatch(w *fsnotify.Watcher, watched map[string]bool, path string) error {
	if watched[path] {
		return nil
	}
	if err := w.Add(path); err != nil {
		return err
	}
	watched[path] = true
	return nil
}

func addWatchIfExists(w *fsnotify.Watcher, watched map[string]bool, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return addWatch(w, watched, path)
}

func markUnwatched(w *fsnotify.Watcher, watched map[string]bool, path string) {
	delete(watched, path)
	_ = w.Remove(path)
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func isRelevantEvent(event fsnotify.Event, canonicalDBPath string) bool {
	if canonicalDBPath == "" {
		return false
	}
	// 1. 严格忽略纯属性/权限变更 (Chmod)，彻底杜绝 SQLite 读操作加锁导致的递归自触发
	if event.Op&fsnotify.Chmod != 0 && event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return false
	}
	// 2. 忽略临时回滚日志文件 (-journal, -shm)
	base := filepath.Base(event.Name)
	if strings.HasSuffix(base, "-journal") || strings.HasSuffix(base, "-shm") {
		return false
	}
	// 3. 仅当数据库主文件 (如 mmssms.db) 或预写日志 (-wal) 发生写入 (Write) 或新建 (Create) 时才视为有效修改
	dbBase := filepath.Base(canonicalDBPath)
	if base == dbBase || base == dbBase+"-wal" {
		return event.Op&(fsnotify.Write|fsnotify.Create) != 0
	}
	return false
}
