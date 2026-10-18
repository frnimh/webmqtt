package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePublisher struct {
	topic, payload string
	calls          int
	err            error
}

func (p *fakePublisher) Publish(_ context.Context, topic, payload string) error {
	p.calls++
	p.topic, p.payload = topic, payload
	return p.err
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHTTPPublishUsesConfiguredPayload(t *testing.T) {
	cfg := Config{Endpoints: testEndpoints()}
	publisher := &fakePublisher{}
	handler := NewHandler(cfg, publisher, NewHistory(cfg.Endpoints), quietLogger())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/publish?payload=override&topic=other", nil))
	if w.Code != 200 || publisher.calls != 1 || publisher.topic != "test/topic" || publisher.payload != "configured payload" {
		t.Fatalf("response=%d, publish=%+v", w.Code, publisher)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("publish response must not be cached")
	}
}

func TestHTTPMethodsAndAuth(t *testing.T) {
	tests := []struct {
		name, method, path, user, password string
		auth                               bool
		status, calls                      int
	}{
		{"open", "GET", "/publish", "", "", false, 200, 1},
		{"missing credentials", "GET", "/publish", "", "", true, 401, 0},
		{"wrong password", "GET", "/publish", "admin", "wrong", true, 401, 0},
		{"wrong username", "GET", "/publish", "wrong", "secret", true, 401, 0},
		{"valid credentials", "GET", "/publish", "admin", "secret", true, 200, 1},
		{"sub requires auth", "GET", "/latest", "", "", true, 401, 0},
		{"unknown", "GET", "/unknown", "", "", false, 404, 0},
		{"exact route", "GET", "/publish/extra", "", "", false, 404, 0},
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"} {
		tests = append(tests, struct {
			name, method, path, user, password string
			auth                               bool
			status, calls                      int
		}{method, method, "/publish", "", "", false, 405, 0})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Endpoints: testEndpoints()}
			if tt.auth {
				cfg.Global.Auth = AuthConfig{Username: "admin", Password: "secret"}
			}
			publisher := &fakePublisher{}
			handler := NewHandler(cfg, publisher, NewHistory(cfg.Endpoints), quietLogger())
			r := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.user != "" {
				r.SetBasicAuth(tt.user, tt.password)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.status || publisher.calls != tt.calls {
				t.Fatalf("response=%d, publish calls=%d", w.Code, publisher.calls)
			}
			if tt.status == 401 && w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing auth challenge")
			}
			if tt.status == 405 && w.Header().Get("Allow") != "GET" {
				t.Fatal("missing Allow header")
			}
		})
	}
}

func TestHTTPHistory(t *testing.T) {
	cfg := Config{Endpoints: testEndpoints()}
	h := NewHistory(cfg.Endpoints)
	handler := NewHandler(cfg, &fakePublisher{}, h, quietLogger())
	for _, value := range []string{"1", "2", "3", "4"} {
		h.Add("test/topic", []byte(value))
	}
	for _, tc := range []struct {
		path  string
		count int
		first string
	}{{"/latest", 1, "4"}, {"/history", 3, "2"}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		var response struct {
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(response.Messages) != tc.count || response.Messages[0].Payload != tc.first {
			t.Fatalf("response=%s", w.Body)
		}
	}
	w := httptest.NewRecorder()
	NewHandler(cfg, &fakePublisher{}, NewHistory(cfg.Endpoints), quietLogger()).ServeHTTP(w, httptest.NewRequest("GET", "/latest", nil))
	var response map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response["messages"]) != "[]" {
		t.Fatalf("empty messages = %s", response["messages"])
	}
}

func TestHTTPPublishFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrUnavailable, 503}, {context.DeadlineExceeded, 504}, {errors.New("broker rejected"), 502}} {
		cfg := Config{Endpoints: testEndpoints()}
		handler := NewHandler(cfg, &fakePublisher{err: tc.err}, NewHistory(cfg.Endpoints), quietLogger())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/publish", nil))
		if w.Code != tc.status {
			t.Fatalf("error %v returned %d", tc.err, w.Code)
		}
	}
}
