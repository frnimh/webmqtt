//go:build integration

package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func TestMQTTIntegration(t *testing.T) {
	brokerPath, err := exec.LookPath("mosquitto")
	if err != nil {
		t.Skip("install mosquitto to run live MQTT integration tests")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "mosquitto.conf")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("listener %d 127.0.0.1\nallow_anonymous true\npersistence false\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	var broker *exec.Cmd
	stopBroker := func() {
		if broker == nil {
			return
		}
		if err := broker.Process.Signal(os.Interrupt); err != nil {
			t.Logf("stop broker: %v", err)
		}
		if err := broker.Wait(); err != nil {
			t.Logf("broker exit: %v", err)
		}
		broker = nil
	}
	startBroker := func() {
		broker = exec.Command(brokerPath, "-c", configPath)
		broker.Stdout, broker.Stderr = io.Discard, io.Discard
		if err := broker.Start(); err != nil {
			t.Fatal(err)
		}
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		eventually(t, 5*time.Second, func() bool {
			conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		})
	}
	t.Cleanup(stopBroker)
	startBroker()
	cfg := Config{Global: GlobalConfig{MQTT: MQTTConfig{Host: "127.0.0.1", Port: port}}, Endpoints: testEndpoints()}
	history := NewHistory(cfg.Endpoints)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	backend, err := ConnectMQTT(ctx, cfg.Global.MQTT, history, quietLogger())
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	handler := NewHandler(cfg, backend, history, quietLogger())
	for range 4 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/publish", nil))
		if w.Code != 200 {
			t.Fatalf("publish: %d %s", w.Code, w.Body)
		}
	}
	eventually(t, 5*time.Second, func() bool { return len(history.Last("test/topic", 3)) == 3 })
	// A separate MQTT client verifies that SUB also captures external publishers.
	external := mqtt.NewClient(mqtt.NewClientOptions().AddBroker(cfg.Global.MQTT.BrokerURL()).SetClientID("webmqtt-integration-external").SetAutoReconnect(false))
	if token := external.Connect(); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("external MQTT connection failed")
	}
	t.Cleanup(func() { external.Disconnect(0) })
	if token := external.Publish("test/topic", 1, false, "external payload"); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("external MQTT publish failed")
	}
	eventually(t, 5*time.Second, func() bool {
		messages := history.Last("test/topic", 1)
		return len(messages) == 1 && messages[0].Payload == "external payload"
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/latest", nil))
	var response struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 1 || response.Messages[0].Payload != "external payload" {
		t.Fatalf("GET /latest = %s", w.Body)
	}
	stopBroker()
	eventually(t, 5*time.Second, func() bool { return !backend.ready.Load() })
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/publish", nil))
	if w.Code != 503 {
		t.Fatalf("offline publish = %d", w.Code)
	}
	startBroker()
	eventually(t, 20*time.Second, func() bool { return backend.ready.Load() })
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/publish", nil))
	if w.Code != 200 {
		t.Fatalf("publish after reconnect = %d %s", w.Code, w.Body)
	}
	eventually(t, 5*time.Second, func() bool {
		messages := history.Last("test/topic", 1)
		return len(messages) == 1 && messages[0].Payload == "configured payload"
	})
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
