package bridge

import (
	"os"
	"strings"
	"testing"
)

func TestSampleConfig(t *testing.T) {
	cfg, err := LoadConfig("../../config.sample.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Global.Port != 8080 || len(cfg.Endpoints) != 3 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !strings.Contains(cfg.Endpoints[1].MQTT.Payload, `"key1": "value1"`) {
		t.Fatal("multiline payload lost")
	}
}

func TestConfigValidation(t *testing.T) {
	sample, err := os.ReadFile("../../config.sample.yml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, old, replacement string
		valid                  bool
	}{
		{"null auth", "auth: #basic auth\n    username: admin\n    password: admin", "auth:", true},
		{"empty auth", "username: admin\n    password: admin", "username: ''\n    password: ''", true},
		{"no auth", "  auth: #basic auth\n    username: admin\n    password: admin\n", "", true},
		{"partial auth", "password: admin", "password: ''", false},
		{"invalid web port", "port: 8080", "port: 0", false},
		{"invalid mqtt port", "port: 1883", "port: 65536", false},
		{"empty mqtt host", "host: mqtt.example.com", "host: ''", false},
		{"url host", "host: mqtt.example.com", "host: tcp://localhost", false},
		{"duplicate routes", "/api/v1/resource2", "/api/v1/resource1", false},
		{"query path", "/api/v1/resource2", "/api/v1/resource2?x=1", false},
		{"relative path", "/api/v1/resource2", "resource2", false},
		{"unknown operation", "method: PUB", "method: POST", false},
		{"lowercase operation", "method: PUB", "method: pub", true},
		{"zero history", "last: 1", "last: 0", false},
		{"negative history", "last: 1", "last: -1", false},
		{"excessive history", "last: 1", "last: 10001", false},
		{"wildcard topic", "topic: topic1", "topic: topic/+", false},
		{"unknown field", "last: 1", "last: 1\n      typo: true", false},
		{"second document", "last: 1", "last: 1\n---\nglobal: {}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeConfig(strings.NewReader(strings.ReplaceAll(string(sample), tt.old, tt.replacement)))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestMQTTSSLConfig(t *testing.T) {
	sample, err := os.ReadFile("../../config.sample.yml")
	if err != nil {
		t.Fatal(err)
	}
	const sslBlock = "    ssl:\n      enabled: false\n      verify: true\n"
	tests := []struct {
		name, ssl       string
		enabled, verify bool
		valid           bool
	}{
		{name: "omitted", verify: true, valid: true},
		{name: "empty", ssl: "    ssl: {}\n", verify: true, valid: true},
		{name: "null", ssl: "    ssl:\n", verify: true, valid: true},
		{name: "disabled", ssl: sslBlock, verify: true, valid: true},
		{name: "enabled default verify", ssl: "    ssl:\n      enabled: true\n", enabled: true, verify: true, valid: true},
		{name: "enabled verify", ssl: "    ssl:\n      enabled: true\n      verify: true\n", enabled: true, verify: true, valid: true},
		{name: "enabled no verify", ssl: "    ssl:\n      enabled: true\n      verify: false\n", enabled: true, valid: true},
		{name: "disabled no verify", ssl: "    ssl:\n      enabled: false\n      verify: false\n", valid: true},
		{name: "invalid enabled", ssl: "    ssl:\n      enabled: invalid\n"},
		{name: "invalid verify", ssl: "    ssl:\n      verify: invalid\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := DecodeConfig(strings.NewReader(strings.Replace(string(sample), sslBlock, tt.ssl, 1)))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
			if !tt.valid {
				return
			}
			if got := cfg.Global.MQTT.SSL; got.Enabled != tt.enabled || got.Verify != tt.verify {
				t.Fatalf("ssl = %+v, want enabled=%v verify=%v", got, tt.enabled, tt.verify)
			}
			wantURL := "tcp://mqtt.example.com:1883"
			if tt.enabled {
				wantURL = "ssl://mqtt.example.com:1883"
			}
			if got := cfg.Global.MQTT.BrokerURL(); got != wantURL {
				t.Fatalf("broker URL = %s, want %s", got, wantURL)
			}
		})
	}
}
