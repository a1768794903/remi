# Remi Go migration phase plan

This phase continues the full migration from `backend/` to `server/`. The
Python source remains the behavioral reference; each implemented surface must
have a Go test and must use the relational schema rather than a compatibility
stub.

## Current phase

- Align Ent edge storage with the checked-in MySQL migration.
- Add conversation count, finalize, and finalization-status behavior.
- Add the authenticated multipart STT proxy contract.
- Run focused tests, then the complete Go test and build suite.

## Follow-on inventory

The remaining Python routers are tracked from `backend/main.py` and the
individual `backend/routers/*.py` decorators. Memory, chat/LLM, sync,
integrations, account, payment, desktop, MCP, and background-worker surfaces
remain migration work until their behavior and tests exist in `server/`.
