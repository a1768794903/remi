import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_blue_plus/flutter_blue_plus.dart';

import '../protocol/ble_protocol.dart';

enum WearableStatus { disconnected, scanning, connecting, connected }

class WearableConnection extends ChangeNotifier {
  WearableStatus status = WearableStatus.disconnected;
  List<ScanResult> discovered = const [];
  BluetoothDevice? device;
  int? batteryLevel;
  String? firmwareVersion;
  AudioCodec? audioCodec;
  String? error;

  final _audioPackets = StreamController<List<int>>.broadcast();
  Stream<List<int>> get audioPackets => _audioPackets.stream;

  StreamSubscription<List<ScanResult>>? _scanSubscription;
  StreamSubscription<BluetoothConnectionState>? _connectionSubscription;
  StreamSubscription<List<int>>? _audioSubscription;
  bool _disposed = false;

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  Future<void> scan() async {
    if (status == WearableStatus.scanning) return;
    error = null;
    discovered = const [];
    status = WearableStatus.scanning;
    _notify();
    await _scanSubscription?.cancel();
    _scanSubscription = FlutterBluePlus.scanResults.listen((results) {
      discovered = results.where((result) {
        return result.advertisementData.serviceUuids.any(
          (uuid) => uuid.toString().toLowerCase() == BleProtocol.service,
        );
      }).toList();
      _notify();
    });
    try {
      await FlutterBluePlus.startScan(
        withServices: [Guid(BleProtocol.service)],
        timeout: const Duration(seconds: 8),
      );
      await FlutterBluePlus.isScanning.where((scanning) => !scanning).first;
    } catch (cause) {
      error = 'Unable to scan: $cause';
    } finally {
      if (status == WearableStatus.scanning) {
        status = WearableStatus.disconnected;
      }
      _notify();
    }
  }

  Future<void> connect(BluetoothDevice selected) async {
    if (await FlutterBluePlus.isScanning.first) {
      await FlutterBluePlus.stopScan();
    }
    await _scanSubscription?.cancel();
    _scanSubscription = null;
    await disconnect();
    status = WearableStatus.connecting;
    device = selected;
    error = null;
    _notify();
    try {
      await selected.connect(timeout: const Duration(seconds: 20));
      final services = await selected.discoverServices();
      final audio = _characteristic(
        services,
        BleProtocol.service,
        BleProtocol.audioData,
      );
      if (audio == null) {
        throw StateError('Wearable audio characteristic not found');
      }

      batteryLevel = await _readByte(
        services,
        BleProtocol.batteryService,
        BleProtocol.batteryLevel,
      );
      firmwareVersion = await _readText(
        services,
        BleProtocol.deviceInfoService,
        BleProtocol.firmwareRevision,
      );
      final codecId = await _readByte(
        services,
        BleProtocol.service,
        BleProtocol.audioCodec,
      );
      audioCodec = codecId == null ? null : AudioCodec.fromId(codecId);

      _audioSubscription = audio.onValueReceived.listen((packet) {
        if (packet.length > BleProtocol.audioHeaderBytes) {
          _audioPackets.add(packet);
        }
      });
      await audio.setNotifyValue(true);
      _connectionSubscription = selected.connectionState.listen((state) {
        if (state == BluetoothConnectionState.disconnected &&
            device?.remoteId == selected.remoteId) {
          status = WearableStatus.disconnected;
          _notify();
        }
      });
      status = WearableStatus.connected;
      _notify();
    } catch (cause) {
      error = 'Unable to connect: $cause';
      await disconnect();
    }
  }

  Future<void> disconnect() async {
    await _audioSubscription?.cancel();
    _audioSubscription = null;
    await _connectionSubscription?.cancel();
    _connectionSubscription = null;
    final previous = device;
    device = null;
    batteryLevel = null;
    firmwareVersion = null;
    audioCodec = null;
    status = WearableStatus.disconnected;
    _notify();
    if (previous != null) {
      try {
        await previous.disconnect();
      } catch (cause) {
        error = 'Unable to disconnect cleanly: $cause';
        _notify();
      }
    }
  }

  BluetoothCharacteristic? _characteristic(
    List<BluetoothService> services,
    String serviceId,
    String characteristicId,
  ) {
    for (final service in services) {
      if (service.uuid.toString().toLowerCase() != serviceId) continue;
      for (final characteristic in service.characteristics) {
        if (characteristic.uuid.toString().toLowerCase() == characteristicId) {
          return characteristic;
        }
      }
    }
    return null;
  }

  Future<int?> _readByte(
    List<BluetoothService> services,
    String serviceId,
    String characteristicId,
  ) async {
    final characteristic = _characteristic(
      services,
      serviceId,
      characteristicId,
    );
    if (characteristic == null) return null;
    try {
      final value = await characteristic.read();
      return value.isEmpty ? null : value.first;
    } catch (_) {
      return null;
    }
  }

  Future<String?> _readText(
    List<BluetoothService> services,
    String serviceId,
    String characteristicId,
  ) async {
    final characteristic = _characteristic(
      services,
      serviceId,
      characteristicId,
    );
    if (characteristic == null) return null;
    try {
      final value = await characteristic.read();
      return value.isEmpty ? null : String.fromCharCodes(value).trim();
    } catch (_) {
      return null;
    }
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    if (_scanSubscription != null) unawaited(_scanSubscription!.cancel());
    if (_audioSubscription != null) unawaited(_audioSubscription!.cancel());
    if (_connectionSubscription != null) {
      unawaited(_connectionSubscription!.cancel());
    }
    if (device != null) unawaited(device!.disconnect());
    unawaited(_audioPackets.close());
    super.dispose();
  }
}
