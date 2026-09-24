# Omi 源码调查与 Remi MVP 架构基线

> 调查日期：2026-09-24
>
> 范围：只读检查 `omi/app`、`omi/backend`、`omi/sdks`、`omi/omi/firmware`。本文件不表示 Remi 依赖 Omi 运行；Remi 必须复制或重写所需合同后独立构建。

## 结论摘要

Omi 的可复用主链路已经比较清晰：

```text
Omi wearable
  └─ BLE GATT audio notify
       └─ Flutter native BLE transport / OmiConnection
            └─ 16 kHz mono audio frames
                 └─ WebSocket /v4/listen
                      └─ Omi backend listen pipeline
                           └─ STT provider
                                └─ transcript events
                                     └─ conversation lifecycle / post-processing
                                          └─ summary, memories, action items
```

Remi 第一阶段应保留 BLE 和音频兼容性，重写服务端编排与业务存储：Go API/WebSocket、MySQL、Redis、可替换 STT/LLM provider。Firebase 只暂时承担 App 登录与 ID Token 签发，不作为 Remi 业务数据库。

## 1. Wearable 如何连接 Flutter

Flutter 入口位于：

- `omi/app/lib/services/devices/discovery/native_bluetooth_discoverer.dart`：通过原生 Bluetooth/Pigeon 能力扫描设备。
- `omi/app/lib/services/devices/connectors/omi_connection.dart`：连接 Omi 设备，发现 GATT characteristic，读取 codec、battery、firmware，并订阅 audio notify。
- `omi/app/lib/services/devices/transports/native_ble_transport.dart`：抽象原生 BLE 读、写、通知和连接状态。
- `omi/app/lib/services/capture/capture_controller.dart`：获取连接设备的 codec，创建转写 WebSocket，并将设备音频帧转发到 socket。

Remi 应复制 BLE transport、设备发现、`OmiConnection` 的音频/电量/固件最小子集；对用户可见文本和类名以 Remi 为主，但第一阶段可在协议兼容层保留 Omi 内部标识。

## 2. BLE Protocol 在哪里

协议的跨 SDK 单一说明位于 `omi/sdks/device/PROTOCOL.md`，对应实现包括：

- `omi/sdks/device/dart/lib/uuids.dart` 与 `omi_device.dart`
- `omi/sdks/device/go/omidevice/protocol.go`
- `omi/sdks/python/omi/constants.py`、`ble.py`、`decoder.py`
- Flutter 的 `omi/app/lib/services/devices/connectors/omi_connection.dart`
- Firmware 的 `omi/omi/firmware/devkit/src/config.h` 与 `omi/omi/firmware/omi/src/lib/core/config.h`

第一阶段不要修改 UUID、packet framing、codec ID 或已有 command；Remi 协议目录应从这些文件提取并由 Remi 自己维护。

## 3. Audio packet format

已确认的 BLE GATT 合同：

| 项目 | 值 | 证据 |
|---|---|---|
| Service | `19b10000-e8f2-537e-4f6c-d104768a1214` | `sdks/device/PROTOCOL.md` |
| Audio notify characteristic | `19b10001-e8f2-537e-4f6c-d104768a1214` | 同上 |
| Codec read characteristic | `19b10002-e8f2-537e-4f6c-d104768a1214` | 同上 |
| Battery service / level | `0000180f...` / `00002a19...` | 同上 |
| Packet | 3-byte header + codec payload | 同上、`sdks/python/omi/decoder.py` |
| PCM output | signed 16-bit little-endian, mono, 16 kHz | 同上 |
| Codec ID 0 / 1 | PCM16 / PCM8 | 同上 |
| Codec ID 20 | Opus, DevKit, 160 samples / 10 ms | 同上 |
| Codec ID 21 | Opus FS320, Omi CV1, 320 samples / 20 ms | 同上 |

注意：SDK 的 `OPUS_FRAME_SAMPLES = 960` 是 decode buffer 上限，不是所有固件的 wire frame size。Go 解码器必须依据 codec ID 区分 160 和 320 sample frame 的时序。

## 4. Flutter 如何上传 Audio

`omi/app/lib/services/sockets/transcription_service.dart` 构造：

```text
{http/https API base}
  -> {ws/wss}
  -> /v4/listen
  -> ?language=...&sample_rate=...&codec=...&uid=...
     &include_speech_profile=...
     &stt_service=...
     &conversation_timeout=...
     [&source=...]
     [&client_conversation_id=...]
     [&custom_stt=enabled]
     [&speaker_auto_assign=enabled]
     [&create_speakers=...]
     [&vad_gate=enabled]
```

`PureSocket` 使用 Firebase 认证结果生成的请求 headers，连接成功后将设备/手机产生的二进制音频帧直接写入 WebSocket。连接还会发送客户端状态 JSON；服务端下行消息由 `TranscriptionService` 解码为 transcript segment/event。

`capture_controller.dart` 是 BLE 音频到 socket 的主要连接点。Remi Phase 1 应尽量保留该数据流，不要先重写音频转码或 capture ownership。

## 5. Backend 哪个接口收 Audio

实时音频入口是 WebSocket `/v4/listen`，不是普通 REST upload。App 的主要调用位置是：

- `app/lib/services/sockets/transcription_service.dart`
- `app/lib/services/capture/capture_controller.dart`
- `app/lib/services/sockets/pure_socket.dart`

Omi backend 的 listen 实现分布在 `backend/routers/listen/`，相关 receiver、pusher、realtime demand 和生命周期模块共同完成接收、转写事件分发与连接清理。Remi 需要实现兼容入口，至少保留上面的 query 参数、Firebase token 验证、binary audio input、JSON transcript output 和正常关闭语义。

## 6. STT 流程

Omi App 支持两类路径：默认服务端 `/v4/listen` 流式转写，以及 custom STT/on-device/polling 分支。MVP 只需要默认服务端路径。Omi 的 provider 选择通过 `stt_service` 等参数和 backend STT 工具模块完成；SDK README 也展示了 Deepgram 实时转写路径。

Remi Go 只负责 provider orchestration，不重写 VAD、diarization 或 ML framework。首个接口应是：

```go
type STTProvider interface {
    StartStream(ctx context.Context, meta StreamMetadata) error
    WriteAudio(ctx context.Context, pcm []byte) error
    Close(ctx context.Context) error
}
```

实际 provider 放在 `server/internal/stt`，优先实现 Deepgram-compatible provider；没有 key 时允许 dev/null provider 让链路和协议可测试。

## 7. Conversation、Summary、Memory、Todo

Omi App 的 conversation API 调用集中在：

- `app/lib/backend/http/api/conversations.dart`
- `app/lib/backend/http/api/memories.dart`
- `app/lib/backend/http/api/action_items.dart`
- `app/lib/backend/http/api/users.dart`（daily summary）

Omi backend 的 REST route 分布在 `backend/routers/conversations.py`、`memories.py`、`action_items.py`、用户/summary 相关 router；持久化实现分布在 `backend/database/`，底层主要是 Firestore。

Remi 不应逐行翻译这些 Python 实现。Remi 合同应简化为：一个连续 transcript session 在静默 `CONVERSATION_GAP_MINUTES` 后关闭；关闭时由 LLM 返回结构化 title/summary/memories/todos；结果分别写入 MySQL 的 Conversation、Memory、Todo 表，并通过 App REST API 查询。

## 8. Authentication 与存储边界

App 的 `auth_provider.dart` 使用 Firebase Auth 的 auth/id-token changes；公共 HTTP helper (`backend/http/shared.dart`) 为 API/WebSocket 请求构造认证 headers。Omi backend 以当前用户 UID 作为请求身份并使用 Firestore 保存用户及业务数据。

Remi 第一阶段边界：

- Firebase Auth：保留登录和 ID Token 验证。
- Go auth middleware：验证 Firebase ID Token，得到 user ID。
- MySQL 8：User、Device、Conversation、TranscriptSegment、Memory、Todo 的业务主存储。
- Redis：会话指针、连接协调、短期状态/队列；不替代 MySQL 事实数据。
- GCS/Firestore/其他 Omi 云服务：不作为 Remi MVP 的运行时依赖。

## 9. 明确排除

不复制 `desktop/`、`omiGlass/`、`plugins/`、`personas/`、`marketplace/`、screen capture、Windows/macOS desktop 路径。Omi Flutter 中的 Apple Watch、Ray-Ban、Omi Glass、手机通话等 connector 也不属于 Remi Wearable MVP。

## 10. 待通过硬件/集成测试验证的事项

源码可以确认格式和参数，但以下事项仍需要真实设备/后端合同测试确认：

1. 当前 Omi CV1 实际广播名称和 Remi App 的扫描过滤策略。
2. `/v4/listen` 每类下行 JSON event 的完整 schema、关闭码和断线重连行为。
3. 服务端接收的是原始 BLE codec payload 还是 App 已转成 PCM 的 payload；App 支持两种 STT 分支，Remi 必须以 capture path 集成测试为准。
4. Firebase token 在 WebSocket 握手中的 header 名称、过期重试和匿名/冷启动行为。
5. Omi firmware storage/OTA command 的所有版本兼容差异；Phase 1 只保留必要的 battery/firmware/status 展示。

