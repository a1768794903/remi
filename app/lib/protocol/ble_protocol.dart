/// Omi-compatible GATT values for the first Remi wearable release.
/// Keep in sync with ../../protocol/README.md until this is generated.
abstract final class BleProtocol {
  static const service = '19b10000-e8f2-537e-4f6c-d104768a1214';
  static const audioData = '19b10001-e8f2-537e-4f6c-d104768a1214';
  static const audioCodec = '19b10002-e8f2-537e-4f6c-d104768a1214';
  static const batteryService = '0000180f-0000-1000-8000-00805f9b34fb';
  static const batteryLevel = '00002a19-0000-1000-8000-00805f9b34fb';
  static const deviceInfoService = '0000180a-0000-1000-8000-00805f9b34fb';
  static const firmwareRevision = '00002a26-0000-1000-8000-00805f9b34fb';

  static const audioHeaderBytes = 3;
  static const pcmSampleRate = 16000;
  static const pcmChannels = 1;
}

enum AudioCodec {
  pcm16(0),
  pcm8(1),
  opus(20),
  opusFs320(21);

  const AudioCodec(this.id);
  final int id;

  static AudioCodec? fromId(int id) {
    for (final codec in values) {
      if (codec.id == id) return codec;
    }
    return null;
  }
}

List<int> stripAudioHeader(List<int> packet) {
  if (packet.length <= BleProtocol.audioHeaderBytes) return const [];
  return packet.sublist(BleProtocol.audioHeaderBytes);
}
