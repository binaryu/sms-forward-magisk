package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
		title:   "来自 {{from}} 的短信",
		msgType: "info",
		format:  "text",
		client:  server.Client(),
	}

	record := smsRecord{
		ID:      1,
		Address: "10086",
		Body:    "您的余额为 100 元",
	}

	err := sender.SendSMS(context.Background(), record)
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}

	if receivedPayload.Title != "来自 10086 的短信" {
		t.Errorf("expected title '来自 10086 的短信', got '%s'", receivedPayload.Title)
	}
	expectedBody := "发信人: 10086\n内容: 您的余额为 100 元"
	if receivedPayload.Body != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, receivedPayload.Body)
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

	// Test special chars like quotes and newlines
	record := smsRecord{
		ID:      2,
		Address: "+8613800138000",
		Body:    "验证码是: \"123456\"\n请勿泄露给他人",
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
		rawURL: server.URL + "/push?sender={{from}}&text={{body}}",
		method: "GET",
		client: server.Client(),
	}

	record := smsRecord{
		ID:      3,
		Address: "10010",
		Body:    "hello world",
	}

	err := sender.SendSMS(context.Background(), record)
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}

	expectedURI := "/push?sender=10010&text=hello+world"
	if requestedURI != expectedURI {
		t.Errorf("expected '%s', got '%s'", expectedURI, requestedURI)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	os.Clearenv()

	_, _, err := loadConfig("")
	if err == nil {
		t.Errorf("expected error when no destination is configured")
	}

	// Set Webhook
	os.Setenv("WEBHOOK_URL", "https://api.example.com/webhook")
	cfg, _, err := loadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WebhookURL != "https://api.example.com/webhook" {
		t.Errorf("expected WebhookURL to match")
	}
}
