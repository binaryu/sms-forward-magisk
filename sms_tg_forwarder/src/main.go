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
	defaultDBPath1    = "/data/data/com.android.providers.telephony/databases/mmssms.db"
	defaultDBPath2    = "/data/user/0/com.android.providers.telephony/databases/mmssms.db"
	defaultDBPath3    = "/data/user_de/0/com.android.providers.telephony/databases/mmssms.db"
	defaultLastIDPath = "./last_id.txt"
	defaultCACertDir  = "/system/etc/security/cacerts"
	debounceDelay     = 800 * time.Millisecond
)

type config struct {
	DBPath           string
	LastIDPath       string
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
	CACertFile       string
	CACertDir        string
	TLSInsecure      bool
}

type smsRecord struct {
	ID      int64
	Address string
	Body    string
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
	text := fmt.Sprintf("Sender: %s\nContent: %s", record.Address, record.Body)
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
	title := s.title
	if title == "" {
		title = fmt.Sprintf("收到来自 %s 的短信", record.Address)
	} else {
		title = strings.ReplaceAll(title, "{{from}}", record.Address)
		title = strings.ReplaceAll(title, "{{FROM}}", record.Address)
	}

	body := fmt.Sprintf("发信人: %s\n内容: %s", record.Address, record.Body)

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
	reqURL := s.rawURL
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
	reqURL = strings.ReplaceAll(reqURL, "{{timestamp}}", strconv.FormatInt(time.Now().Unix(), 10))

	var bodyReader io.Reader
	headerMap := parseHeaders(s.headers)

	if s.method != http.MethodGet {
		tmpl := s.bodyTemplate
		if tmpl == "" {
			tmpl = `{"from":"{{from}}","content":"{{body}}","timestamp":{{timestamp}}}`
		}

		isJSON := strings.HasPrefix(strings.TrimSpace(tmpl), "{") ||
			strings.HasPrefix(strings.TrimSpace(tmpl), "[") ||
			strings.Contains(strings.ToLower(headerMap.Get("Content-Type")), "application/json")

		addressVal := record.Address
		bodyVal := record.Body
		if isJSON {
			addressVal = jsonEscape(addressVal)
			bodyVal = jsonEscape(bodyVal)
		}

		bodyStr := tmpl
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
		bodyStr = strings.ReplaceAll(bodyStr, "{{timestamp}}", strconv.FormatInt(time.Now().UnixMilli(), 10))
		bodyStr = strings.ReplaceAll(bodyStr, "{{time}}", time.Now().Format("2006-01-02 15:04:05"))

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

func scanDatabases() {
	log.Printf("[SCAN] Scanning Android directories for SMS/MMS databases...")
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
					var name string
					if err := rows.Scan(&name); err == nil {
						var cnt int64
						_ = db.QueryRow("SELECT count(*) FROM " + name).Scan(&cnt)
						if cnt > 0 {
							tables = append(tables, fmt.Sprintf("%s(%d)", name, cnt))
						}
					}
				}
				rows.Close()

				log.Printf("[SCAN] DB: %s\n       Tables: %s", dbFile, strings.Join(tables, ", "))

				for _, tbl := range []string{"sms", "raw", "messages", "mms", "parts"} {
					colRows, err := db.Query("PRAGMA table_info(" + tbl + ")")
					if err == nil {
						hasAddr := false
						hasBody := false
						hasMsgBody := false
						for colRows.Next() {
							var cid int
							var colName, colType string
							var notNull, pk int
							var dflt sql.NullString
							if err := colRows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err == nil {
								if colName == "address" {
									hasAddr = true
								}
								if colName == "body" {
									hasBody = true
								}
								if colName == "message_body" {
									hasMsgBody = true
								}
							}
						}
						colRows.Close()

						bodyCol := "body"
						if !hasBody && hasMsgBody {
							bodyCol = "message_body"
						}

						if hasAddr && (hasBody || hasMsgBody) {
							var id int64
							var addr, bdy string
							q := fmt.Sprintf("SELECT _id, address, %s FROM %s ORDER BY _id DESC LIMIT 1", bodyCol, tbl)
							if err := db.QueryRow(q).Scan(&id, &addr, &bdy); err == nil {
								log.Printf("       ↳ [%s] Latest record: _id=%d, from=%s, body=%s", tbl, id, addr, bdy)
							}
						}
					}
				}
				db.Close()
			}
		}
	}
	if foundCount == 0 {
		log.Printf("[SCAN] No matching database files found in standard locations.")
	}
}

func inspectDatabaseTables(db *sql.DB) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' OR type='view'")
	if err != nil {
		return
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			var count int64
			_ = db.QueryRow("SELECT count(*) FROM " + name).Scan(&count)
			if count > 0 {
				details = append(details, fmt.Sprintf("%s(%d)", name, count))
			}
		}
	}
	log.Printf("[DB INSPECT] Non-empty tables in DB: %s", strings.Join(details, ", "))
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
					log.Printf("[INIT] Detected candidate DB: %s (contains %d messages in 'sms' table)", dbFile, count)
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

func initTimezone() {
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		time.Local = loc
	}
}

// In-memory message deduplication ring buffer (holds hash of last 30 sent messages)
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

func main() {
	initTimezone()

	var configPath string
	var testMode bool
	var scanMode bool
	flag.StringVar(&configPath, "config", "", "path to config.env")
	flag.BoolVar(&testMode, "test", false, "send the latest SMS as a test")
	flag.BoolVar(&scanMode, "scan", false, "scan Android directories for all SMS databases")
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

	realDBPath, smsCount, err := findActiveDatabase(cfg.DBPath)
	if err != nil {
		return err
	}
	cfg.DBPath = realDBPath
	log.Printf("[INIT] Using SMS database: %s (total messages: %d)", cfg.DBPath, smsCount)

	var db *sql.DB
	for attempt := 1; attempt <= 30; attempt++ {
		db, err = openSQLiteDB(cfg.DBPath)
		if err == nil {
			err = db.PingContext(ctx)
			if err == nil {
				break
			}
			db.Close()
		}
		log.Printf("[WAIT] Waiting for Android storage to unlock (%v)... attempt %d/30", err, attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return fmt.Errorf("open sqlite database %s after retries: %w", cfg.DBPath, err)
	}
	defer db.Close()

	sender, err := newSenders(cfg)
	if err != nil {
		return err
	}

	if testMode {
		log.Printf("[TEST] Querying latest SMS to test forwarding...")
		var testRecord smsRecord
		err := db.QueryRowContext(ctx, "SELECT _id, address, body FROM sms ORDER BY _id DESC LIMIT 1").Scan(&testRecord.ID, &testRecord.Address, &testRecord.Body)
		if err != nil {
			log.Printf("[TEST] No SMS found in database, sending simulated test message...")
			testRecord = smsRecord{
				ID:      9999,
				Address: "TEST_BOT",
				Body:    "这是一条来自 SMS Forwarder 的测试短信通知！配置正确！",
			}
		}
		log.Printf("[TEST] Sending SMS (_id=%d from %s): %s", testRecord.ID, testRecord.Address, testRecord.Body)
		if err := sender.SendSMS(ctx, testRecord); err != nil {
			return fmt.Errorf("test send failed: %w", err)
		}
		log.Printf("[TEST SUCCESS] Test message sent successfully!")
		return nil
	}

	lastID, err := loadOrInitializeLastID(ctx, db, cfg.LastIDPath)
	if err != nil {
		return err
	}
	log.Printf("[INIT] Starting from last processed _id=%d (previous SMS will not be resent, waiting for new SMS)", lastID)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	defer watcher.Close()

	dbPath, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	canonicalDBPath, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		canonicalDBPath = dbPath
	}
	walPath := canonicalDBPath + "-wal"
	journalPath := canonicalDBPath + "-journal"
	watchDir := filepath.Dir(canonicalDBPath)

	watched := map[string]bool{}
	if err := addWatch(watcher, watched, watchDir); err != nil {
		return err
	}
	if err := addWatchIfExists(watcher, watched, canonicalDBPath); err != nil {
		return err
	}
	if err := addWatchIfExists(watcher, watched, walPath); err != nil {
		return err
	}
	if err := addWatchIfExists(watcher, watched, journalPath); err != nil {
		return err
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
	_ = os.Remove(testTriggerPath)

	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)

	process := func() {
		nextLastID, err := processNewMessages(ctx, db, sender, cfg.LastIDPath, lastID)
		if err != nil {
			log.Printf("[ERROR] Process new messages: %v", err)
			return
		}
		lastID = nextLastID
	}

	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
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
		log.Printf("[HOT-RELOAD SUCCESS] Configuration reloaded and applied successfully!")
	}

	log.Printf("[READY] Watching SMS directory: %s", watchDir)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hupChan:
			log.Printf("[SIGNAL] Received SIGHUP, reloading configuration...")
			doReload()
		case <-configDebounce.C:
			doReload()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if _, err := os.Stat(testTriggerPath); err == nil {
				_ = os.Remove(testTriggerPath)
				log.Printf("[TEST TRIGGER] Found %s, sending latest SMS...", testTriggerPath)
				var testRecord smsRecord
				if err := db.QueryRowContext(ctx, "SELECT _id, address, body FROM sms ORDER BY _id DESC LIMIT 1").Scan(&testRecord.ID, &testRecord.Address, &testRecord.Body); err == nil {
					_ = sender.SendSMS(ctx, testRecord)
				}
				continue
			}

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

			if !isRelevantEvent(event, canonicalDBPath) {
				continue
			}

			log.Printf("[DB EVENT] SMS database activity detected (%s on %s)", event.Op, filepath.Base(event.Name))

			if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
				markUnwatched(watcher, watched, event.Name)
			}
			if err := addWatchIfExists(watcher, watched, canonicalDBPath); err != nil {
				log.Printf("[WARN] Watch database file: %v", err)
			}
			if err := addWatchIfExists(watcher, watched, walPath); err != nil {
				log.Printf("[WARN] Watch wal file: %v", err)
			}
			resetTimer(debounce, debounceDelay)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("[ERROR] Watcher error: %v", err)
		case <-debounce.C:
			process()
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

	cfg := config{
		DBPath:           get("DB_PATH", ""),
		LastIDPath:       get("LAST_ID_PATH", defaultLastIDPath),
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
	}

	tlsInsecure, _ := strconv.ParseBool(get("TLS_INSECURE_SKIP_VERIFY", "false"))
	cfg.TLSInsecure = tlsInsecure

	appriseUseProxy, _ := strconv.ParseBool(get("APPRISE_USE_PROXY", "false"))
	cfg.AppriseUseProxy = appriseUseProxy

	webhookUseProxy, _ := strconv.ParseBool(get("WEBHOOK_USE_PROXY", "false"))
	cfg.WebhookUseProxy = webhookUseProxy

	hasTelegram := cfg.TelegramBotToken != "" && cfg.TelegramChatID != ""
	hasApprise := cfg.AppriseURL != ""
	hasWebhook := cfg.WebhookURL != ""

	if !hasTelegram && !hasApprise && !hasWebhook {
		return config{}, actualConfigPath, errors.New("at least one destination must be configured: set APPRISE_URL, WEBHOOK_URL, or (TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID)")
	}

	return cfg, actualConfigPath, nil
}

func loadOrInitializeLastID(ctx context.Context, db *sql.DB, path string) (int64, error) {
	var maxID int64
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(_id), 0) FROM sms`).Scan(&maxID)

	content, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(content))
		lastID, err := strconv.ParseInt(value, 10, 64)
		if err == nil && lastID > 0 && lastID <= maxID {
			// Anti-flood: If gap between stored lastID and current maxID is greater than 3,
			// fast-forward directly to maxID to avoid replaying hundreds of old SMS!
			if maxID-lastID > 3 {
				log.Printf("[ANTI-FLOOD] Gap between last_id (%d) and max_id (%d) is %d messages. Fast-forwarding to %d to prevent spamming old messages!", lastID, maxID, maxID-lastID, maxID)
				_ = writeLastID(path, maxID)
				return maxID, nil
			}
			return lastID, nil
		}
	}

	// Default: always start from current maxID
	_ = writeLastID(path, maxID)
	return maxID, nil
}

func processNewMessages(ctx context.Context, db *sql.DB, sender Sender, lastIDPath string, lastID int64) (int64, error) {
	var totalCount int64
	var maxIDInDB int64
	_ = db.QueryRowContext(ctx, "SELECT count(*), COALESCE(MAX(_id), 0) FROM sms").Scan(&totalCount, &maxIDInDB)
	log.Printf("[PROCESS] Checking new SMS... (Current last_id=%d, total in DB=%d, max _id in DB=%d)", lastID, totalCount, maxIDInDB)

	// If database was truncated or messages deleted so that lastID exceeds maxID in DB:
	// Automatically adjust lastID to maxIDInDB!
	if lastID > maxIDInDB {
		log.Printf("[WARN] Stored last_id (%d) > current max_id in DB (%d). Adjusting last_id to %d", lastID, maxIDInDB, maxIDInDB)
		_ = writeLastID(lastIDPath, maxIDInDB)
		return maxIDInDB, nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT _id, type, address, body
		FROM sms
		WHERE _id > ?
		ORDER BY _id ASC
	`, lastID)
	if err != nil {
		return lastID, fmt.Errorf("query sms records after _id=%d: %w", lastID, err)
	}
	defer rows.Close()

	foundCount := 0
	currentLastID := lastID

	for rows.Next() {
		foundCount++
		var id int64
		var msgType int
		var address, body string
		if err := rows.Scan(&id, &msgType, &address, &body); err != nil {
			log.Printf("[WARN] Scan sms row error: %v", err)
			continue
		}

		log.Printf("[FOUND] Found SMS row in DB: _id=%d, type=%d, from=%s, body=%s", id, msgType, address, body)

		// msgType == 2 is user outgoing sent SMS; skip it so we don't echo sent messages
		if msgType == 2 {
			log.Printf("[SKIP] Skipping outgoing/sent SMS (_id=%d, type=2)", id)
			currentLastID = id
			_ = writeLastID(lastIDPath, id)
			continue
		}

		// Deduplication check: never forward identical message twice within 50 messages
		if isDuplicate(address, body) {
			log.Printf("[DEDUP] Skipping already-sent identical message (_id=%d from %s)", id, address)
			currentLastID = id
			_ = writeLastID(lastIDPath, id)
			continue
		}

		record := smsRecord{
			ID:      id,
			Address: address,
			Body:    body,
		}

		log.Printf("[SENDING] Dispatching SMS _id=%d from %s to notification channels...", id, address)
		if err := sender.SendSMS(ctx, record); err != nil {
			log.Printf("[WARN] Failed to send SMS _id=%d: %v", id, err)
			_ = writeLastID(lastIDPath, id)
			currentLastID = id
			return currentLastID, fmt.Errorf("send sms _id=%d: %w", id, err)
		}

		_ = writeLastID(lastIDPath, id)
		currentLastID = id
		log.Printf("[FORWARD SUCCESS] SMS _id=%d from %s forwarded successfully!", id, address)
	}
	if err := rows.Err(); err != nil {
		return currentLastID, fmt.Errorf("iterate sms records: %w", err)
	}

	if foundCount == 0 {
		log.Printf("[PROCESS] No new SMS found with _id > %d", lastID)
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

	if cfg.WebhookURL != "" {
		transport := directTransport
		if cfg.WebhookUseProxy && proxyTransport != nil {
			transport = proxyTransport
		}
		method := strings.ToUpper(strings.TrimSpace(cfg.WebhookMethod))
		if method == "" {
			method = http.MethodPost
		}
		senders = append(senders, &webhookSender{
			rawURL:       cfg.WebhookURL,
			method:       method,
			headers:      cfg.WebhookHeaders,
			bodyTemplate: cfg.WebhookBody,
			client: &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			},
		})
	}

	if len(senders) == 1 {
		return senders[0], nil
	}
	return &multiSender{senders: senders}, nil
}

func newTLSConfig(cfg config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.TLSInsecure {
		tlsConfig.InsecureSkipVerify = true
		return tlsConfig, nil
	}

	certPool, err := loadRootCAs(cfg.CACertFile, cfg.CACertDir)
	if err != nil {
		return nil, err
	}
	if certPool != nil {
		tlsConfig.RootCAs = certPool
	}

	return tlsConfig, nil
}

func loadRootCAs(certFile, certDir string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	loaded := false
	if certFile != "" {
		ok, err := appendCertFile(pool, certFile)
		if err != nil {
			return nil, err
		}
		loaded = loaded || ok
	}

	if certDir != "" {
		ok, err := appendCertDir(pool, certDir)
		if err != nil {
			return nil, err
		}
		loaded = loaded || ok
	}

	if !loaded && certFile == "" && certDir == "" {
		return nil, nil
	}

	return pool, nil
}

func appendCertDir(pool *x509.CertPool, dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read CA certificate directory %s: %w", dir, err)
	}

	loaded := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ok, err := appendCertFile(pool, filepath.Join(dir, entry.Name()))
		if err != nil {
			log.Printf("load CA certificate %s: %v", entry.Name(), err)
			continue
		}
		loaded = loaded || ok
	}

	return loaded, nil
}

func appendCertFile(pool *x509.CertPool, path string) (bool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read CA certificate file %s: %w", path, err)
	}

	return pool.AppendCertsFromPEM(pemData), nil
}

func addWatchIfExists(watcher *fsnotify.Watcher, watched map[string]bool, path string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return addWatch(watcher, watched, path)
}

func addWatch(watcher *fsnotify.Watcher, watched map[string]bool, path string) error {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve watch path %s: %w", path, err)
	}
	if watched[absolutePath] {
		return nil
	}
	if err := watcher.Add(absolutePath); err != nil {
		return fmt.Errorf("add watch %s: %w", absolutePath, err)
	}
	watched[absolutePath] = true
	return nil
}

func markUnwatched(watcher *fsnotify.Watcher, watched map[string]bool, path string) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	delete(watched, absolutePath)
	_ = watcher.Remove(absolutePath)
}

func isRelevantEvent(event fsnotify.Event, dbPath string) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}

	base := filepath.Base(event.Name)
	dbBase := filepath.Base(dbPath)
	return strings.HasPrefix(base, dbBase)
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}
