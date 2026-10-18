# webmqtt

A small Go HTTP server that publishes to MQTT and exposes recent MQTT messages.
Configuration follows [config.sample.yml](config.sample.yml). Only HTTP GET is accepted;
all other HTTP methods, including HEAD, return `405` with `Allow: GET`.

## How it works

1. Read and validate the YAML configuration.
2. Connect to the MQTT broker and subscribe to every topic used by a `SUB` endpoint.
3. Start HTTP on `0.0.0.0:<global.port>` after subscriptions are acknowledged.
4. On GET to a `PUB` endpoint, publish its configured payload to its configured topic.
5. On GET to a `SUB` endpoint, return up to `mqtt.last` cached messages for its topic.

`endpoints[].method` selects the MQTT operation (`PUB` or `SUB`, case insensitive).
It is separate from the HTTP method, which is always GET. Routes match exact paths.
Query parameters do not change the configured topic or payload. YAML multiline
payloads are sent as their decoded string, including any trailing newline.

The server keeps a rolling history in memory, ordered oldest to newest. Reading it
does not consume messages. Each topic keeps at most the largest `last` requested by
its SUB endpoints; `last` must be between 1 and 10,000. History starts when the
server subscribes, resets when the process restarts, and is separate for each
instance. MQTT does not provide arbitrary past-message retrieval. A broker's
retained message, if present, is delivered on subscription and enters the history.

## Configuration

```yaml
global:
  port: 8080
  auth: {} # No authentication. Can also be omitted or left null.
  mqtt:
    host: localhost
    port: 1883
    username: ""
    password: ""
    ssl:
      enabled: false
      verify: true

endpoints:
  - name: turn-on
    path: /light/on
    method: PUB
    mqtt:
      topic: home/light
      payload: "ON"
  - name: recent-state
    path: /light/state
    method: SUB
    mqtt:
      topic: home/light
      last: 3
```

To enable Basic Auth for every configured endpoint:

```yaml
  auth:
    username: admin
    password: change-me
```

Both credentials must be nonempty, or both empty to disable authentication.
Partial credentials are rejected at startup. Use HTTPS at your reverse proxy when
exposing Basic Auth beyond a trusted network. MQTT uses MQTT 3.1.1,
QoS 1, and non-retained publishes. `mqtt.host` is a hostname or unbracketed IP
address without a scheme or port. Topics must be exact names; wildcard filters
are not supported. Unknown config fields, duplicate paths, invalid ports, and
invalid SUB history limits are rejected at startup.

Set `global.mqtt.ssl.enabled: true` to connect over TLS (TLS 1.2 or newer), and
set `global.mqtt.port` to your broker's TLS port, commonly `8883`:

```yaml
global:
  mqtt:
    host: mqtt.example.com
    port: 8883
    ssl:
      enabled: true
      verify: true
```

This is a fragment to merge into the full configuration above. `ssl.enabled`
defaults to `false`, preserving plain TCP for existing configs. `ssl.verify`
defaults to `true` and checks the broker's certificate chain and hostname using
the system's trusted CAs. Setting it to `false` keeps encryption but skips those
checks, allowing self-signed or otherwise untrusted certificates. It has no effect
when SSL is disabled. The configured port is always used; enabling SSL does not
change it automatically.

## Run locally

Requires Go 1.25 or newer and a reachable MQTT broker.

```sh
cp config.sample.yml config.yml
# Edit config.yml with your broker and credentials.
go run ./cmd/webmqtt -config config.yml
```

The config path defaults to `config.yml`. Set `WEBMQTT_CONFIG` to change that
default; `-config` takes precedence. The file is read once at startup.

With the sample endpoint paths and credentials:

```sh
curl -u admin:admin http://localhost:8080/api/v1/resource1
curl -u admin:admin http://localhost:8080/api/v1/resource3
```

A PUB response is returned after the broker acknowledges publication:

```json
{"name":"endpoint1","status":"published","topic":"topic1"}
```

A SUB response looks like this; payloads remain strings even if they contain JSON:

```json
{
  "name": "endpoint3",
  "topic": "topic1",
  "messages": [
    {"topic":"topic1","payload":"text","received_at":"2026-10-17T12:00:00Z"}
  ]
}
```

No received messages returns `"messages": []`. SUB serves its existing cache
during a broker outage. PUB returns `503` while disconnected, `504` if its
five-second acknowledgement deadline expires, and `502` for other MQTT errors.
QoS 1 permits duplicates; a timeout does not prove a message was never delivered.
Repeated GETs to PUB endpoints cause repeated publications. Responses use
`Cache-Control: no-store`.

Initial broker or subscription failure stops startup. After a successful startup,
the client reconnects automatically and resubscribes after connection loss.
Messages published while disconnected are not recovered, except retained messages
delivered on resubscription. Subscription failures are logged and PUB stays
unavailable until a subsequent connection successfully establishes subscriptions.
SIGINT/SIGTERM drains active HTTP requests before disconnecting MQTT. Logs go to
stdout as JSON.

## Run with Docker

```sh
cp config.sample.yml config.yml
# Edit config.yml with a broker reachable from inside the container.
docker compose up --build -d
docker compose logs -f
```

The image runs as a non-root user. The configuration is mounted read-only and is
not copied into the image. The mounted file must be readable by the container's
user (UID 65532). `localhost` inside a container refers to the container itself;
use a broker's Compose service name, network address, or `host.docker.internal`
for a broker running on the Docker Desktop host. If you change `global.port`,
update the container port in the Compose `ports` mapping too.

Alternatively:

```sh
docker build -t webmqtt .
docker run --rm -p 8080:8080 \
  --mount type=bind,src="$(pwd)/config.yml",dst=/etc/webmqtt/config.yml,readonly \
  webmqtt
```

## GitHub Actions releases

The Docker workflow follows the same release pattern as
[SNI-Proxy](https://github.com/frnimh/SNI-Proxy/blob/main/.github/workflows/build-docker.yaml).
Pushing a tag matching `v*.*.*`, such as `v1.0.0`, runs race-enabled Go tests and
`go vet`, then builds and publishes images for `linux/amd64` and `linux/arm64`:

- Docker Hub: `frnimh/webmqtt:latest` and `frnimh/webmqtt:v1.0.0`.
- GHCR: `ghcr.io/<repository-owner>/webmqtt:latest` and the same version tag.

Configure the repository Actions secrets `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN` with credentials that can push to `frnimh/webmqtt`.
GHCR uses the automatically supplied `GITHUB_TOKEN`; the workflow grants it
`packages: write` permission. No separate GHCR secret is needed.

## Development

```sh
make build
make test
make vet
make lint # Requires golangci-lint v2.
```

Live integration testing starts a temporary local Mosquitto broker, exercises
HTTP publish, external MQTT publish, history responses, broker loss, and
resubscription after restart. Install `mosquitto`, then run:

```sh
go test -race -tags=integration ./...
```

The integration test skips if Mosquitto is unavailable. The only direct runtime
dependencies are Eclipse Paho MQTT and YAML v3; HTTP, authentication, logging,
history, and shutdown use the Go standard library.
