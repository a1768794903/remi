# Omi wearable compatibility source

These files are copied from the Omi Flutter app as the first migration slice:

- device model and capability definitions;
- generic device connection contract;
- Omi wearable connection implementation;
- generic/native BLE transport contracts;
- native Bluetooth discovery.

The files retain their upstream package imports and are intentionally kept
outside the active Remi build until their dependencies are migrated together.
This preserves the tested source while allowing the small Remi app to remain
independently buildable. The active Phase 1 adapter is
`app/lib/services/wearable_connection.dart`.

Source: `omi/app/lib/services/devices/`.

