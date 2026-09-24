# Remi Go Server Implementation Plan

> **For agentic workers:** Implement this plan task-by-task and verify each task before continuing.

**Goal:** Build the first independently testable Remi Go backend under `server/`.

**Architecture:** go-zero registers HTTP routes, Gorilla WebSocket handles the binary stream, and small internal packages isolate config, auth, audio session tracking, health checks, and Ent schema definitions. MySQL and Redis are provisioned by Docker Compose for the next persistence phase.

**Tech Stack:** Go 1.24, go-zero, Gorilla WebSocket, Ent, MySQL 8.4, Redis 7.4, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-09-24-remi-go-server-design.md`

## Global Constraints

- All new Go code lives under `remi/server`.
- `omi` remains read-only.
- `remi/backend` is reference source and is not imported by the Go server.
- The first WebSocket phase counts audio bytes and packets before STT integration.
- The six business entities are defined before repository implementation.

## Tasks

### Task 1: Configuration and health

Create `server/internal/config/config.go`, `config_test.go`, `server/internal/health/health.go`, and `health_test.go`. Read HTTP address, database, Redis, auth mode, and conversation gap from environment with deterministic defaults. Expose `/healthz` and `/readyz`; health is process liveness, readiness is dependency readiness.

### Task 2: Audio session tracking

Create `server/internal/audio/session.go` and `session_test.go`. Track session ID, user ID, device ID, codec, sample rate, timestamps, bytes, and packets. `Finish` must remove the session so closed connections cannot keep accumulating state.

### Task 3: Auth and WebSocket transport

Create `server/internal/auth/auth.go`, `server/internal/websocket/handler.go`, and tests. Dev mode supplies a deterministic user for local use or reads `X-Remi-User-ID`; non-dev mode rejects missing identity. The handler accepts binary frames and returns session/audio statistics on `/v1/audio/stream` and `/v4/listen`.

### Task 4: API bootstrap and schemas

Create `server/internal/api/server.go`, `cmd/api/main.go`, `cmd/worker/main.go`, and all six Ent schema files. Register go-zero routes and preserve the schema relationships and fields documented in the migration plan.

### Task 5: Runtime packaging

Create `server/go.mod`, `Dockerfile`, `docker-compose.yml`, `Makefile`, and `README.md`. Verify `go test ./...`, local health endpoints, and the debug API image build when Docker is available.
