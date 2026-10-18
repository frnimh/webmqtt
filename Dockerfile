FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/webmqtt ./cmd/webmqtt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/webmqtt /webmqtt
EXPOSE 8080
ENTRYPOINT ["/webmqtt"]
CMD ["-config", "/etc/webmqtt/config.yml"]
