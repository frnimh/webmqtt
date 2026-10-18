# Go development

For every Go task, load `golang-how-to` and use it to select the relevant Go skills.

Keep the service small: entry point in `cmd/webmqtt`, implementation and tests in
`internal/bridge`, and manual constructor wiring. Configuration is defined by
`config.sample.yml`.

Run `go test -race ./...` and `go vet ./...` after changes. When Mosquitto is
available, run `go test -race -tags=integration ./...` for live broker coverage.
