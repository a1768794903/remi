# Remi protocol SDK snapshot

This directory is a source-preserving migration of `omi/sdks` into the Remi
repository. It contains the device protocol SDKs for Dart, Go, Python,
TypeScript/React Native, Rust, and Swift, together with their tests and
protocol documentation.

The source snapshot is used as the initial compatibility baseline. Remi code
must import or adapt protocol definitions from this directory rather than
reintroducing BLE UUIDs throughout Flutter business code.

The upstream Omi project is MIT licensed. The repository level license and all
upstream license/copyright files must remain with copied substantial source
code. Any later Remi changes should be documented in the relevant SDK
directory or in `docs/MIGRATION_PLAN.md`.

The copied SDKs are not all part of the Flutter build yet. Phase 1 currently
uses the Remi Flutter BLE adapter in `app/lib/services/wearable_connection.dart`;
the copied Dart SDK is the next integration source for replacing duplicated
protocol constants.

