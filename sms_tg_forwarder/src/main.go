package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	_ "modernc.org/sqlite"
)

const (
	defaultDBPath     = "/data/user_de/0/com.android.providers.telephony/databases/mmssms.db"
	defaultLastIDPath = "./last_id.txt"
	defaultProxyURL   = "http://127.0.0.1:7890"
	defaultCACertDir  = "/system/etc/security/cacerts"
	debounceDelay     = time.Second
)

type config struct {
	DBPath           string
	LastIDPath       string
	ProxyURL         string
	TelegramBotToken string
	TelegramChatID   string
	CACertFile       string
	CACertDir        string
	TLSInsecure      bool
}

type smsRecord struct {
	ID      int64
	Address string
	Body    string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}

	lastID, err := loadOrInitializeLastID(ctx, db, cfg.LastIDPath)
	if err != nil {
		return err
	}
	log.Printf("starting from last processed _id=%d", lastID)

	sender, err := newTelegramSender(cfg)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	defer watcher.Close()

	dbPath, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	walPath := dbPath + "-wal"
	watchDir := filepath.Dir(dbPath)

	watched := map[string]bool{}
	if err := addWatch(watcher, watched, watchDir); err != nil {
		return err
	}
	if err := addWatchIfExists(watcher, watched, dbPath); err != nil {
		return err
	}
	if err := addWatchIfExists(watcher, watched, walPath); err != nil {
		return err
	}

	process := func() {
		nextLastID, err := processNewMessages(ctx, db, sender, cfg.LastIDPath, lastID)
		if err != nil {
			log.Printf("process new messages: %v", err)
			return
		}
		lastID = nextLastID
	}

	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}

	log.Printf("watching %s and %s", dbPath, walPath)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !isRelevantEvent(event, dbPath, walPath) {
				continue
			}
			if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
				markUnwatched(watcher, watched, event.Name)
			}
			if err := addWatchIfExists(watcher, watched, dbPath); err != nil {
				log.Printf("watch database file: %v", err)
			}
			if err := addWatchIfExists(watcher, watched, walPath); err != nil {
				log.Printf("watch wal file: %v", err)
			}
			resetTimer(debounce, debounceDelay)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		case <-debounce.C:
			process()
		}
	}
}

func loadConfig() (config, error) {
	cfg := config{
		DBPath:           envOrDefault("DB_PATH", defaultDBPath),
		LastIDPath:       envOrDefault("LAST_ID_PATH", defaultLastIDPath),
		ProxyURL:         envOrDefault("PROXY_URL", defaultProxyURL),
		TelegramBotToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramChatID:   strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		CACertFile:       strings.TrimSpace(os.Getenv("CA_CERT_FILE")),
		CACertDir:        envOrDefault("CA_CERT_DIR", defaultCACertDir),
	}

	tlsInsecure, err := strconv.ParseBool(envOrDefault("TLS_INSECURE_SKIP_VERIFY", "false"))
	if err != nil {
		return config{}, fmt.Errorf("parse TLS_INSECURE_SKIP_VERIFY: %w", err)
	}
	cfg.TLSInsecure = tlsInsecure

	if cfg.TelegramBotToken == "" {
		return config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.TelegramChatID == "" {
		return config{}, errors.New("TELEGRAM_CHAT_ID is required")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func loadOrInitializeLastID(ctx context.Context, db *sql.DB, path string) (int64, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(content))
		if value == "" {
			return 0, fmt.Errorf("%s is empty", path)
		}
		lastID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", path, err)
		}
		return lastID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}

	var maxID int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(_id), 0) FROM sms`).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("initialize last id from database: %w", err)
	}
	if err := writeLastID(path, maxID); err != nil {
		return 0, err
	}
	return maxID, nil
}

func processNewMessages(ctx context.Context, db *sql.DB, sender *telegramSender, lastIDPath string, lastID int64) (int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT _id, address, body
		FROM sms
		WHERE type = 1 AND _id > ?
		ORDER BY _id ASC
	`, lastID)
	if err != nil {
		return lastID, fmt.Errorf("query sms records after _id=%d: %w", lastID, err)
	}
	defer rows.Close()

	currentLastID := lastID
	for rows.Next() {
		var record smsRecord
		if err := rows.Scan(&record.ID, &record.Address, &record.Body); err != nil {
			return currentLastID, fmt.Errorf("scan sms record: %w", err)
		}

		text := fmt.Sprintf("Sender: %s\nContent: %s", record.Address, record.Body)
		if err := sender.sendMessage(ctx, text); err != nil {
			return currentLastID, fmt.Errorf("send sms _id=%d: %w", record.ID, err)
		}

		if err := writeLastID(lastIDPath, record.ID); err != nil {
			return currentLastID, err
		}
		currentLastID = record.ID
		log.Printf("forwarded sms _id=%d", record.ID)
	}
	if err := rows.Err(); err != nil {
		return currentLastID, fmt.Errorf("iterate sms records: %w", err)
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

type telegramSender struct {
	apiURL string
	chatID string
	client *http.Client
}

func newTelegramSender(cfg config) (*telegramSender, error) {
	proxyURL, err := url.Parse(cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}

	tlsConfig, err := newTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &telegramSender{
		apiURL: fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramBotToken),
		chatID: cfg.TelegramChatID,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy:           http.ProxyURL(proxyURL),
				TLSClientConfig: tlsConfig,
			},
		},
	}, nil
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

func (s *telegramSender) sendMessage(ctx context.Context, text string) error {
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

func isRelevantEvent(event fsnotify.Event, dbPath, walPath string) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}

	eventPath, err := filepath.Abs(event.Name)
	if err != nil {
		return false
	}

	return eventPath == dbPath || eventPath == walPath
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
