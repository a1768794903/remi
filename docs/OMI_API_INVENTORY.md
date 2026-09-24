# Omi App API Inventory for Remi

> 调查日期：2026-09-24。此表只记录 App 源码实际可见的 API 依赖，并按 Remi MVP 分级。Omi 的完整接口数量很大；非 MVP 接口明确标记为 SKIP，不作为 Remi 复制目标。

| API / 通道 | App 使用位置 | Omi Backend 实现 | Remi 是否需要 | 优先级 | 状态 |
|---|---|---|---|---|---|
| 用户初始化/账户资料接口（具体路径待逐项核对） | `app/lib/backend/http/api/users.dart`, `user_provider.dart` | 用户 router + Firestore users | YES | P0 | 待核对路径；未发现 App 调用 `GET /v1/users/me` |
| Firebase Auth / ID Token | `app/lib/providers/auth_provider.dart`, `backend/http/shared.dart` | Firebase Auth client + UID dependency | YES（暂保留） | P0 | 复用认证，Go 验证 |
| BLE discovery/connect（本地，不是 HTTP） | `native_bluetooth_discoverer.dart`, `omi_connection.dart` | N/A | YES | P0 | Phase 1 copy minimal |
| BLE audio notify + codec read | `omi_connection.dart`, `capture_controller.dart` | N/A；协议在 `sdks/device/PROTOCOL.md` | YES | P0 | 保持兼容 |
| `WS /v4/listen` binary audio | `transcription_service.dart`, `pure_socket.dart` | `backend/routers/listen/` | YES | P0 | Go compatibility endpoint |
| `/v4/listen` transcript/event JSON | `transcription_service.dart`, `capture_controller.dart` | listen receiver/pusher | YES | P0/P1 | 需定义 Remi wire contract |
| `GET /v1/conversations` | `api/conversations.dart`, `conversation_provider.dart` | `routers/conversations.py` | YES | P1 | TODO-Go contract |
| `GET /v1/conversations/{id}` | `api/conversations.dart` | `routers/conversations.py` | YES | P1 | TODO-Go contract |
| `POST /v1/conversations`（处理 in-progress） | `api/conversations.dart` | conversation processing route | YES | P1 | 可简化为 finalize |
| `POST /v1/conversations/{id}/reprocess` | `api/conversations.dart` | conversation reprocess route | NO（首版） | P3 | SKIP |
| `DELETE /v1/conversations/{id}` | `api/conversations.dart` | `routers/conversations.py` | YES | P1 | TODO-Go contract |
| conversation title/segment edits | `api/conversations.dart` | conversation router/database | NO（首版可延后） | P2 | DEFER |
| `GET /v3/memories` | `api/memories.dart`, `memories_provider.dart` | `routers/memories.py` | YES | P2 | Remi 改为 `/v1/memories` |
| `POST /v3/memories` | `api/memories.dart` | `routers/memories.py` | YES（内部/可选） | P2 | Remi contract |
| `POST /v1/memories/query` | Remi 新需求；Omi 现有 memories search/ledger API 可参考 | Omi memory search/database | YES | P2 | 新建 MySQL FULLTEXT contract |
| memory review/visibility/ledger endpoints | `api/memories.dart` | `routers/memories.py` | NO（首版） | P3 | SKIP |
| action items list/create/update | `api/action_items.dart`, `action_items_provider.dart` | `routers/action_items.py` | YES | P2 | Remi Todo contract |
| daily summary | `api/users.dart`, home providers | user/daily summary backend | YES（聚合字段即可） | P2 | 简化为 Conversation analysis |
| audio playback `/v1/sync/audio/...` | `api/audio.dart`, conversation detail | sync/audio routers + GCS | NO（首版可只显示文本） | P3 | SKIP/DEFER |
| local/file upload `/v2/sync-local-files` | capture/WAL sync | sync routers + object storage | NO | P3 | SKIP |
| custom STT upload/polling | `transcription_polling_service.dart` | STT upload/transcription routes | NO（首版） | P3 | SKIP |
| `/v2/messages` Ask/chat | `api/messages.dart`, `message_provider.dart` | messages/chat router + LLM | YES（重写） | P2 | Remi `/v1/ask` with sources |
| realtime transcript webhook | developer mode provider | webhook router | NO | P3 | SKIP |
| Firebase/Firestore business reads | 不应由 Remi App 直接使用 | Omi database modules | NO | P0 | 禁止迁移到 Remi |
| apps/plugins/personas/marketplace | 多个 app API files | Omi app/plugin/persona routers | NO | P3 | SKIP |
| glasses/watch/camera/phone-call APIs | 对应 specialized connectors | Omi-specific routers | NO | P3 | SKIP |

## MVP 合同草案

Remi 服务端首批应实现的最小接口：

```text
GET  /healthz
GET  /v1/devices
GET  /v1/devices/{id}
WS   /v1/audio/stream       # Phase 3; Phase 1 可兼容 /v4/listen
GET  /v1/conversations
GET  /v1/conversations/{id}
GET  /v1/memories
POST /v1/memories/query
GET  /v1/todos
PATCH /v1/todos/{id}
POST /v1/ask
```

### `/v4/listen` compatibility notes

Phase 1/3 为兼容现有 App，保留 Omi 路径和 query：`language`、`sample_rate`、`codec`、`uid`、`source`、`client_conversation_id`、`conversation_timeout`。Remi 自有 App 后续可迁移到 `/v1/audio/stream`，但不要在设备链路未跑通前改变 BLE 或音频 framing。

### Authentication

所有业务 HTTP 与 WebSocket 都必须由 Firebase ID Token 解析出 user ID；客户端传入的 `uid` 只能作为兼容字段，不能作为授权依据。未认证或 token 无效时，HTTP 返回 401，WebSocket 使用握手/关闭错误而不泄露业务数据。

## Inventory 方法与限制

本表通过扫描 `app/lib/backend/http`、`app/lib/services/sockets`、`app/lib/services/capture`、`app/lib/providers` 的 Dart 源码，并与 `backend/routers`、`backend/database`、`sdks/device/PROTOCOL.md` 对照得出。生成代码、集成服务和开发者设置中还存在大量非 MVP API；它们不会因为出现在 Omi 代码中就自动进入 Remi。
