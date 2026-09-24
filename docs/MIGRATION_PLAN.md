# Omi → Remi Migration Plan

> 目标：以已验证的 BLE/device/audio 能力为兼容基线，建立可独立构建的 Remi 产品。所有复制动作都从 `omi` 到 `remi` 单向进行；不得让 Remi 在构建或运行时依赖 `../omi`。

## 组件映射

| Omi Component | Remi Replacement | 策略 |
|---|---|---|
| `omi/app` Flutter app | `remi/app` Remi Flutter fork | 只复制 MVP device/capture/backend schema/UI 所需代码；随后替换用户可见品牌 |
| `app/.../native_bluetooth_discoverer.dart` | `remi/app/...` device discovery | 保留 BLE 行为，去掉 Glass/watch/非 MVP discoverer |
| `app/.../omi_connection.dart` | `remi/app/...` compatible wearable connection | 保留音频、codec、battery、firmware/status；内部 Omi protocol 名称可暂留 |
| `app/.../capture_controller.dart` + sockets | Remi capture pipeline | 初期复用稳定 capture ownership/socket wiring，后续删除非 MVP branches |
| `sdks/device/PROTOCOL.md` + SDK constants | `remi/sdks` + Remi protocol adapter | 先完整迁移 SDK；Remi App 通过 adapter 使用初始 UUID/framing/codec 合同 |
| `omi/omi/firmware` | `remi/firmware` | 只复制当前硬件的 audio/BLE/battery/flash/OTA 所需固件；保留 MIT notice |
| Omi FastAPI routers/listen | `remi/server/internal/api` + `websocket` | 按 contract 重写 Go，不逐行翻译 Python |
| Omi Python STT provider modules | `remi/server/internal/stt` | `STTProvider` 接口，先接 Deepgram-compatible provider，可用 dev/null 替身 |
| Omi LLM/post-processing | `remi/server/internal/llm` + `conversation` + `summary` | 结构化输出 title/summary/memories/todos；加入 schema validation |
| Firestore users/conversations/memories/action items | MySQL 8 + Ent | 业务事实数据全部迁移到 MySQL；不复制 Firestore database helpers |
| Omi Redis usage | Remi Redis | 只用于 session/lock/cache/queue；加入 TTL 和 fail-safe 策略 |
| Firebase Auth | Firebase Auth + Go token middleware | 第一阶段继续登录；Firebase 不保存 Remi 业务实体 |
| Omi `/v1`/`/v2`/`/v3`/`/v4` REST/WebSocket contracts | Remi Go API | Python 已实现的生产路由、WebSocket、后台任务和独立服务都必须逐项迁移；迁移完成前不能切换生产运行时 |
| Omi `backend/` | Remi `backend/` migration baseline → `server/` Go runtime | 已迁移 Python 源码用于行为/合同对照；生产运行时按 Go 方案重建 |
| Omi `web/admin`, `web/app`, `web/frontend` | Remi `web/` migration baseline | 已迁移源码；后续替换 Remi branding、API 和 MVP 页面 |
| Omi desktop/glasses/plugins/personas/marketplace | 无 | 不复制，不进入 Remi MVP 构建图 |

## 分阶段执行

### Phase 0 — 源码调查（本次）

- [x] 确认 `omi` 与 `remi` 位置；`omi` 保持只读。
- [x] 读取 Omi 根目录、App、Backend、Firmware 指南（存在的文件）。
- [x] 记录 BLE UUID、codec ID、3-byte packet header、16 kHz mono PCM contract。
- [x] 定位 Flutter 的 `/v4/listen` WebSocket、auth headers、conversation/memory/todo API；具体用户初始化路径仍待核对。
- [x] 生成 `OMI_ARCHITECTURE.md`、`OMI_API_INVENTORY.md`、本迁移计划。
- [ ] 用真实设备确认 WebSocket 下行事件全量 schema 与断线行为。

### Phase 1 — Flutter fork / device vertical slice

目标：Remi App 能独立 build，并连接现有 Omi hardware，显示 Connected、Battery、Firmware。

当前进展（2026-09-24）：`remi/sdks` 已完整迁入 Omi `sdks` 目录（440 个上游文件，另加 Remi 迁移说明），并保留 SDK 内的测试、协议文档和许可证文件。Flutter 侧已迁入第一批 Omi wearable 源码到 `app/lib/omi_compat`；这些文件仍保留上游 package imports，待依赖成组迁移。`remi/app` 已有独立 Flutter 3.47.5 Android/iOS 工程、Remi 名称、BLE service 扫描、GATT 连接、battery/firmware/codec 读取、audio notify stream。`flutter analyze`、`flutter test` 与 `flutter build apk --debug` 均通过，APK 产物为 `app/build/app/outputs/flutter-apk/app-debug.apk`。尚未用 Omi CV1 实机验收，故 Phase 1 仍待硬件验收。

后续迁移基线：Omi Python backend 已复制到 `remi/backend`（排除桌面专用入口和 fixture），Omi web 的 admin/app/frontend 已复制到 `remi/web`（排除 Personas 和生成目录）。这些目录用于源码、接口和行为对照；生产 backend 仍按 Go 目标放在 `remi/server`。

最初的 Omi 文件清单如下。源码检查显示 `native_bluetooth_discoverer.dart` 和 `native_ble_transport.dart` 依赖 Pigeon、iOS/Android 原生实现、设备注册与大量非 MVP 类型。Phase 1 将先建立独立 Flutter 工程，按 `sdks/device/PROTOCOL.md` 实现兼容 BLE 连接；后续只有在实机验证发现兼容缺口时，再有选择地迁入 Omi 原生桥接代码：

```text
app/pubspec.yaml, analysis_options.yaml, l10n/config（按 Remi 包名重建）
app/lib/services/devices/discovery/device_discoverer.dart
app/lib/services/devices/discovery/native_bluetooth_discoverer.dart
app/lib/services/devices/discovery/device_locator.dart
app/lib/services/devices/connectors/device_connection.dart
app/lib/services/devices/connectors/omi_connection.dart
app/lib/services/devices/transports/device_transport.dart
app/lib/services/devices/transports/native_ble_transport.dart
app/lib/services/devices/models.dart
app/lib/services/capture/capture_controller.dart（先保留必要路径）
app/lib/services/capture/capture_composition.dart
app/lib/services/sockets/pure_socket.dart
app/lib/services/sockets/transcription_service.dart（仅为链路验证）
app/lib/backend/schema/bt_device/
app/lib/env/ 与 native BLE/Pigeon 最小桥接
```

复制理由：这些文件构成 discovery → GATT connect → codec/battery/device info → audio notify 的垂直链路。不要复制完整 `app/lib`，也不要复制 `omiGlass`、Ray-Ban、Apple Watch、插件、marketplace、personas、desktop 或其生成文件；生成文件应在 Remi 工程内重新生成。

Phase 1 的输出还应包括 Remi adapter 对 `remi/sdks/` 中 UUID/codec/framing 定义的使用，并保留 MIT copyright notice。品牌替换优先覆盖 App 名、icon/splash、theme、navigation、settings/about；内部 `OmiDeviceProtocol` 等名称不在此阶段强制重命名。

### Phase 2 — Go backend parity foundation

```text
remi/server/
  cmd/api/
  cmd/worker/
  internal/{api,auth,device,audio,transcription,conversation,memory,todo,summary,chat,llm,stt,repository,websocket,config}
  ent/
  migrations/
  configs/
  docker/
  docker-compose.yml
  Makefile
```

验收：`docker compose up` 启动 Go API、MySQL、Redis；`GET /healthz` 返回可观测的依赖状态。先建立 Ent schema：User、Device、Conversation、TranscriptSegment、Memory、Todo，并建立 Python 路由/行为 parity 清单。当前仅有 skeleton，不能作为 Python 服务替代品。

### Phase 3 — Audio streaming compatibility

先兼容 Omi App 的 `WS /v4/listen`：解析 query 和 Firebase 身份，统计 received bytes/session duration/device ID/user ID，保存/转发音频帧。只有链路通后才引入新的 `/v1/audio/stream`。

验收：设备 → Flutter → Go 的音频 bytes 可在 Go 日志/metrics 中看到；无 STT key 时 dev provider 仍能完成握手、接收和关闭。

### Phase 4 — STT

在 `internal/stt` 实现 Deepgram provider 与 fake provider；将 codec 解码和 sample metadata 变成明确的 `StreamMetadata`。实时 transcript event 要能通过 WebSocket 返回 Flutter。

### Phase 5 — Conversation

连续 transcript 归属于 session；静默超过 `CONVERSATION_GAP_MINUTES`（默认 10）时关闭 Conversation，写入 TranscriptSegment，App 可分页查看历史。

### Phase 6 — AI analysis

Conversation 完成后异步调用 LLM，严格解析：

```json
{
  "title": "",
  "summary": "",
  "topics": [],
  "memories": [],
  "todos": []
}
```

写入 MySQL；失败不应丢失原始 transcript，允许重试/人工重跑。

### Phase 7 — Ask Remi

`POST /v1/ask` 先查询 MySQL FULLTEXT + recent conversations，再把带 ID 的 context 交给 LLM。响应必须含 answer 和 sources；无命中时固定返回“没有找到相关记录。”，不得无来源 hallucination。

## Phase 1 之后的独立构建约束

1. Remi 的 import/package/path 不得引用 `../omi`。
2. Remi 的 Flutter 用户可见品牌不得继续显示 Omi；协议兼容内部命名可暂保留。
3. 固件复制必须保留上游 MIT notice，并记录复制来源和后续修改。
4. 每个阶段先 build/run/test，再进入下一阶段；不要一次迁移几十个模块。
5. Phase 1 的真实验收是现有 Omi hardware 连接与状态显示，不是完整 AI 功能。
