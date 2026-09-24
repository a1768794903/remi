# Remi app — Phase 1 device slice

This Flutter source tree scans for the Omi-compatible service UUID, connects,
subscribes to the device audio characteristic, and reads battery and firmware
information. Audio packets are exposed as `WearableConnection.audioPackets`
for the later backend streaming phase.

The project has no dependency on `../omi`. The initial BLE wire contract is
documented in `../protocol/README.md`.

## Platform setup

Android/iOS platform projects have been generated with Flutter 3.47.5.
To verify from this directory with a Flutter SDK:

```sh
flutter pub get
flutter analyze
flutter test
```

`flutter analyze`, `flutter test`, and `flutter build apk --debug` passed in the
current workspace. The APK was produced at
`build/app/outputs/flutter-apk/app-debug.apk`. iOS builds require Xcode on
macOS.

BLE permissions are configured in the generated platform projects:

- Android: `BLUETOOTH_SCAN`, `BLUETOOTH_CONNECT`, and legacy location/Bluetooth
  permissions where the supported OS versions require them.
- iOS: `NSBluetoothAlwaysUsageDescription` in `Info.plist`.

Device acceptance remains pending: an Omi CV1 must be scanned, connected,
and verified for battery, firmware, and audio notifications on both platforms.
