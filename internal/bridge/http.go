package bridge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

var ErrUnavailable = errors.New("MQTT connection unavailable")

type Publisher interface {
	Publish(context.Context, string, string) error
}

func NewHandler(cfg Config, publisher Publisher, history *History, logger *slog.Logger) http.Handler {
	routes := make(map[string]Endpoint, len(cfg.Endpoints))
	for _, endpoint := range cfg.Endpoints {
		routes[endpoint.Path] = endpoint
	}
	usernameHash := sha256.Sum256([]byte(cfg.Global.Auth.Username))
	passwordHash := sha256.Sum256([]byte(cfg.Global.Auth.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "only GET is allowed"})
			return
		}
		if cfg.Global.Auth.Username != "" {
			username, password, ok := r.BasicAuth()
			u := sha256.Sum256([]byte(username))
			p := sha256.Sum256([]byte(password))
			validUser := subtle.ConstantTimeCompare(u[:], usernameHash[:])
			validPassword := subtle.ConstantTimeCompare(p[:], passwordHash[:])
			if !ok || validUser&validPassword != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="webmqtt", charset="UTF-8"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
		}
		e, ok := routes[r.URL.Path]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
			return
		}
		if e.Method == "SUB" {
			writeJSON(w, http.StatusOK, struct {
				Name     string    `json:"name"`
				Topic    string    `json:"topic"`
				Messages []Message `json:"messages"`
			}{e.Name, e.MQTT.Topic, history.Last(e.MQTT.Topic, e.MQTT.Last)})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := publisher.Publish(ctx, e.MQTT.Topic, e.MQTT.Payload); err != nil {
			logger.Error("MQTT publish failed", "endpoint", e.Name, "error", err)
			status := http.StatusBadGateway
			if errors.Is(err, ErrUnavailable) {
				status = http.StatusServiceUnavailable
			}
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": e.Name, "topic": e.MQTT.Topic, "status": "published"})
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A response write failure means the client has disconnected; no retry is possible.
	_ = json.NewEncoder(w).Encode(value)
}
