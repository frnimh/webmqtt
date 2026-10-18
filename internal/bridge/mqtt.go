package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MQTT struct {
	client mqtt.Client
	ready  atomic.Bool
}

func ConnectMQTT(ctx context.Context, cfg MQTTConfig, history *History, logger *slog.Logger) (*MQTT, error) {
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("generate MQTT client ID: %w", err)
	}
	m := &MQTT{}
	initial := make(chan error, 1)
	var once sync.Once
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL()).
		SetClientID("webmqtt-" + hex.EncodeToString(id[:])).
		SetUsername(cfg.Username).
		SetPassword(cfg.Password).
		SetProtocolVersion(4).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(false).
		SetConnectTimeout(10 * time.Second).
		SetWriteTimeout(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(5 * time.Second).
		SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		m.ready.Store(false)
		logger.Warn("MQTT disconnected; reconnecting", "error", err)
	})
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, message mqtt.Message) {
		history.Add(message.Topic(), message.Payload())
	})
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		// Resubscribe after every reconnect because each connection uses a clean session.
		filters := make(map[string]byte, len(history.topics))
		for topic := range history.topics {
			filters[topic] = 1
		}
		var err error
		if len(filters) > 0 {
			token := client.SubscribeMultiple(filters, nil)
			if !token.WaitTimeout(10 * time.Second) {
				err = fmt.Errorf("MQTT subscription timed out")
			} else {
				err = token.Error()
			}
			if err == nil {
				for topic, qos := range token.(*mqtt.SubscribeToken).Result() {
					if qos == 0x80 {
						err = fmt.Errorf("MQTT subscription rejected for topic %q", topic)
						break
					}
				}
			}
		}
		m.ready.Store(err == nil && client.IsConnectionOpen())
		if err != nil {
			logger.Error("MQTT subscription failed", "error", err)
		} else {
			logger.Info("MQTT connected and subscriptions ready")
		}
		once.Do(func() { initial <- err })
	})
	m.client = mqtt.NewClient(opts)
	if err := waitToken(ctx, m.client.Connect()); err != nil {
		m.Close()
		return nil, fmt.Errorf("connect MQTT: %w", err)
	}
	select {
	case err := <-initial:
		if err != nil {
			m.Close()
			return nil, err
		}
		return m, nil
	case <-ctx.Done():
		m.Close()
		return nil, fmt.Errorf("initialize MQTT subscriptions: %w", ctx.Err())
	}
}

func (m *MQTT) Publish(ctx context.Context, topic, payload string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.ready.Load() || !m.client.IsConnectionOpen() {
		return ErrUnavailable
	}
	return waitToken(ctx, m.client.Publish(topic, 1, false, payload))
}

func (m *MQTT) Close() {
	m.ready.Store(false)
	m.client.Disconnect(250)
}

func waitToken(ctx context.Context, token mqtt.Token) error {
	select {
	case <-token.Done():
		return token.Error()
	case <-ctx.Done():
		return ctx.Err()
	}
}
