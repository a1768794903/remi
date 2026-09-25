# Go Backend Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Migrate the production behavior exposed by `backend/` into `server/` so the Go service can replace the Python backend without silent placeholder success paths.

**Architecture:** Keep the existing Go service split into small `internal/<domain>` packages, with Ent/MySQL as the durable store, Redis for short-lived claims and queues, and explicit provider interfaces for external services. Use the Python routers and utility modules as the behavioral contract, then add route-level and package-level tests for each migrated surface.

**Tech Stack:** Go, go-zero REST, Ent, MySQL, Redis, Gorilla WebSocket, Go standard library HTTP clients, and package-level tests with `httptest`.

**Spec:** `backend/routers/` and the corresponding `backend/utils/` implementations.

## Global Constraints

- All new Go implementation lives under `server/`.
- Python remains unchanged and is the behavior reference.
- User data must be isolated by authenticated UID on every read, update, delete, queue status, and download path.
- External-provider behavior must fail explicitly when configuration is absent; no fake success responses.
- Every new behavior is covered by a failing test before implementation and verified with `gofmt`, `go test ./...`, `go build ./...`, and `git diff --check` from `server/`.
- Do not commit or push until the migration inventory is complete and the full verification pass is green.

## Review Focus

- A manifest from another UID/device/conversation must not authorize an upload.
- A queue/status/download request must not expose another user's files or jobs.
- Missing Redis/MySQL/provider configuration must produce an explicit failure, not an empty success.
- Range requests and pending audio artifacts must preserve the Python HTTP status/content contract.
- Retries and duplicate submissions must be idempotent rather than creating duplicate durable records.

### Migration batches

1. Sync capture manifests, audio precache/URLs/download, and durable sync job state.
2. Conversation finalization post-processing: summaries, memories, action items, and integration hooks.
3. Integration OAuth/provider registry, callback/state/PKCE, and token refresh.
4. Payments and subscription state (Stripe/PayPal) with webhook idempotency.
5. Apps/personas/MCP/calendar/desktop proxy and prompt endpoints.
6. Chat files, voice, sharing, quota, tool calling, and retrieval.
7. Memory ledger, review queue, imports, vector search, and belief model.
8. Remaining admin/announcement/advice/agent/auth compatibility routes, exact response contracts, and Python→Go inventory tests.
9. MySQL/Redis integration verification, deployment smoke tests, and only then commit/push.
