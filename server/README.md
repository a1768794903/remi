# Remi Go server

This directory is the production backend target for Remi. It is independent
of `../backend`, which is the migrated Omi Python source used for contract
comparison.

## Current phase

The first skeleton provides:

- go-zero HTTP server;
- `GET /healthz` and `GET /readyz`;
- `WS /v1/audio/stream`;
- Omi-compatible `WS /v4/listen` path;
- development authentication via `X-Remi-User-ID` or `dev-user`;
- audio session byte and packet counters;
- complete Ent schema definitions for User, Device, Conversation,
  TranscriptSegment, Memory, and Todo;
- checked-in MySQL migration at `migrations/001_init.sql`;
- MySQL, Redis, and API Docker Compose services.

The WebSocket persists conversation sessions and final STT segments through
the configured provider. The current Go migration also exposes action-item,
conversation lifecycle/search, transcript, memory CRUD, and authenticated
multipart STT proxy routes. Firebase token verification, durable
finalization workers, LLM analysis, and the remaining Python router families
are still migration work and are not represented as completed here.

## Run locally

```sh
go test ./...
go run ./cmd/api
curl http://localhost:8080/healthz
docker compose up --build
```

## Database schema

The model definitions live in `ent/schema`. Generate Ent runtime code with
`make generate-ent` when the generated client is needed by a repository layer.
The deployable initial MySQL schema is checked in at
`migrations/001_init.sql`; apply it once after creating the `remi` database.
The Ent generator is pinned to `GOARCH=amd64` because the upstream generator
uses 32-bit-overflowing constants and cannot compile as a 386 tool, even though
the server packages themselves remain portable.
