# Remi backend migration baseline

This directory is a source-preserving migration of the Omi Python backend
into the Remi repository. It is available for contract and behavior analysis
while the production Remi backend is rebuilt under `server/` in Go.

Migrated areas include the HTTP routers, listen/transcription pipeline,
conversation and memory logic, tests, deployment configuration, and backend
documentation. Desktop-only backend entrypoints and fixtures were excluded
from this Remi product repository.

The copied Python service still references Omi's Firebase/Firestore and cloud
configuration. It must not be used as Remi's production runtime without the
planned contract migration to Go, MySQL, Redis, Ent, and the Remi auth
boundary. The source is retained so behavior can be compared while each
Remi contract is implemented.

Source: `omi/backend`.

