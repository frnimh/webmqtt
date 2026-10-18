package bridge

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Global    GlobalConfig `yaml:"global"`
	Endpoints []Endpoint   `yaml:"endpoints"`
}

type GlobalConfig struct {
	Port int        `yaml:"port"`
	Auth AuthConfig `yaml:"auth"`
	MQTT MQTTConfig `yaml:"mqtt"`
}

type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type MQTTConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Endpoint struct {
	Name   string       `yaml:"name"`
	Path   string       `yaml:"path"`
	Method string       `yaml:"method"`
	MQTT   EndpointMQTT `yaml:"mqtt"`
}

type EndpointMQTT struct {
	Topic   string `yaml:"topic"`
	Payload string `yaml:"payload"`
	Last    int    `yaml:"last"`
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	return DecodeConfig(f)
}

func DecodeConfig(r io.Reader) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return cfg, errors.New("config must contain exactly one YAML document")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Global.Port < 1 || c.Global.Port > 65535 {
		return errors.New("global.port must be between 1 and 65535")
	}
	auth := c.Global.Auth
	if (auth.Username == "") != (auth.Password == "") {
		return errors.New("global.auth requires both username and password, or neither")
	}
	if strings.Contains(auth.Username, ":") {
		return errors.New("global.auth.username cannot contain a colon")
	}
	m := c.Global.MQTT
	if m.Host == "" || strings.ContainsAny(m.Host, "/?#@ \t\r\n") || (strings.Contains(m.Host, ":") && net.ParseIP(m.Host) == nil) {
		return errors.New("global.mqtt.host must be a hostname or IP address without a scheme or port")
	}
	if m.Port < 1 || m.Port > 65535 {
		return errors.New("global.mqtt.port must be between 1 and 65535")
	}
	if len(c.Endpoints) == 0 {
		return errors.New("at least one endpoint is required")
	}
	paths := make(map[string]bool)
	for i := range c.Endpoints {
		e := &c.Endpoints[i]
		prefix := fmt.Sprintf("endpoint %d (%s)", i+1, e.Name)
		u, err := url.ParseRequestURI(e.Path)
		if err != nil || !strings.HasPrefix(e.Path, "/") || strings.HasPrefix(e.Path, "//") || u.RawQuery != "" || strings.ContainsAny(e.Path, "?#") || u.Path != e.Path {
			return fmt.Errorf("%s: path must be an absolute URI path without query, fragment, or escapes", prefix)
		}
		if paths[e.Path] {
			return fmt.Errorf("%s: duplicate path %q", prefix, e.Path)
		}
		paths[e.Path] = true
		e.Method = strings.ToUpper(e.Method)
		if e.Method != "PUB" && e.Method != "SUB" {
			return fmt.Errorf("%s: method must be PUB or SUB", prefix)
		}
		if e.MQTT.Topic == "" || len(e.MQTT.Topic) > 65535 || !utf8.ValidString(e.MQTT.Topic) || strings.ContainsAny(e.MQTT.Topic, "+#\x00") {
			return fmt.Errorf("%s: mqtt.topic must be a nonempty exact MQTT topic without wildcards", prefix)
		}
		if e.Method == "SUB" && (e.MQTT.Last < 1 || e.MQTT.Last > 10000) {
			return fmt.Errorf("%s: mqtt.last must be between 1 and 10000", prefix)
		}
	}
	return nil
}

func (c MQTTConfig) BrokerURL() string {
	return "tcp://" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}
