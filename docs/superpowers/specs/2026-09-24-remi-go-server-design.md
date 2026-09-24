# Remi Go Server Design

## Goal

Create an independent Go backend under `remi/server` that can accept the Remi
audio stream, expose health endpoints, and define the complete first-version
business schema before STT and AI processing are added.

## Boundaries

`remi/backend` remains the copied Omi Python source used for contract
comparison. The new runtime does not import it. The Go service owns the API,
WebSocket transport, authentication boundary, repositories, and later
conversation orchestration.

## First implementation

- go-zero owns the HTTP server and route registration.
- Gorilla WebSocket owns the binary audio transport.
- `internal/audio` tracks session metadata and received bytes.
- `internal/auth` exposes a development identity boundary and reserves the
  Firebase verification mode.
- Ent schemas define User, Device, Conversation, TranscriptSegment, Memory,
  and Todo with relationships and constraints.
- Docker Compose provides MySQL 8.4, Redis 7.4, and the API container.

## Success criteria

`go test ./...` passes; `/healthz` returns 200; the WebSocket accepts binary
frames and emits counters; all six Ent schemas compile; and the API image can
be built from `server/Dockerfile`.
