-- Remi initial relational schema for MySQL 8.
-- Keep this migration aligned with ent/schema. Ent remains the source of
-- application models; this checked-in SQL is useful for deployment and review.

CREATE TABLE IF NOT EXISTS `users` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_uid` varchar(255) NOT NULL,
    `email` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL DEFAULT '',
    `language` varchar(16) NOT NULL DEFAULT 'en',
    `time_zone` varchar(128) NOT NULL DEFAULT 'UTC',
    `onboarding` json NULL,
    `private_cloud_sync_enabled` boolean NOT NULL DEFAULT TRUE,
    `meeting_note_screenshots_enabled` boolean NOT NULL DEFAULT TRUE,
    `store_recording_permission` boolean NOT NULL DEFAULT FALSE,
    `daily_summary_enabled` boolean NOT NULL DEFAULT TRUE,
    `daily_summary_hour_local` int NOT NULL DEFAULT 22,
    `mentor_notification_frequency` int NOT NULL DEFAULT 0,
    `integrations` json NULL,
    `notification_settings` json NULL,
    `assistant_settings` json NULL,
    `ai_profile` json NULL,
    `data_protection_level` varchar(32) NOT NULL DEFAULT 'standard',
    `migration_status` json NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `users_external_uid` (`external_uid`),
    UNIQUE KEY `users_email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `account_deletion_wipes` (
    `job_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `status` varchar(32) NOT NULL DEFAULT 'pending',
    `attempts` int NOT NULL DEFAULT 0,
    `last_error` varchar(512) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `completed_at` datetime(6) NULL,
    PRIMARY KEY (`job_id`),
    KEY `account_deletion_wipes_uid_status` (`user_external_uid`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `feedback_events` (
    `event_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `surface` varchar(64) NOT NULL,
    `target_kind` varchar(64) NOT NULL,
    `target_id` varchar(255) NOT NULL,
    `value` int NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`event_id`),
    KEY `feedback_events_user_created` (`user_external_uid`,`created_at`),
    KEY `feedback_events_target` (`target_kind`,`target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `account_cutover` (
    `uid` varchar(255) NOT NULL,
    `state` varchar(32) NOT NULL DEFAULT 'legacy',
    `account_generation` bigint NOT NULL DEFAULT 0,
    `ui_generation` bigint NOT NULL DEFAULT 0,
    `api_generation` bigint NOT NULL DEFAULT 0,
    `stranded_new_data` boolean NOT NULL DEFAULT false,
    `offline_queue_instruction` varchar(32) NOT NULL DEFAULT 'none',
    `checkpoint_phase` varchar(64) NOT NULL DEFAULT 'not_started',
    `checkpoint_token` varchar(128) NOT NULL DEFAULT '',
    `manifest_id` varchar(128) NOT NULL DEFAULT '',
    `destination_backend_bound` boolean NOT NULL DEFAULT false,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `hume_callbacks` (
    `job_id` varchar(255) NOT NULL,
    `status` varchar(64) NOT NULL DEFAULT '',
    `payload` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`job_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `hume_emotion_predictions` (
    `job_id` varchar(255) NOT NULL,
    `sequence` int NOT NULL,
    `begin_seconds` double NOT NULL DEFAULT 0,
    `end_seconds` double NOT NULL DEFAULT 0,
    `emotions` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`job_id`,`sequence`),
    KEY `hume_predictions_time` (`job_id`,`begin_seconds`,`end_seconds`),
    CONSTRAINT `hume_predictions_callback` FOREIGN KEY (`job_id`) REFERENCES `hume_callbacks` (`job_id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `hume_expression_jobs` (
    `job_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `conversation_id` bigint NULL,
    `task_action` varchar(64) NOT NULL DEFAULT 'hume_emotion_detection',
    `status` varchar(32) NOT NULL DEFAULT 'queued',
    `top_emotions` json NULL,
    `follow_up_status` varchar(32) NOT NULL DEFAULT 'pending',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`job_id`),
    KEY `hume_expression_jobs_user` (`user_external_uid`,`created_at`),
    KEY `hume_expression_jobs_conversation` (`conversation_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `chat_first_intents` (
    `intent_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `continuity_key` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `source` varchar(64) NOT NULL,
    `payload` json NOT NULL,
    `delivery_state` varchar(32) NOT NULL DEFAULT 'ready',
    `created_at` datetime(6) NOT NULL,
    `delivered_at` datetime(6) NULL,
    `fetch_count` int NOT NULL DEFAULT 0,
    `materialization_attempts` int NOT NULL DEFAULT 0,
    `last_rejection_code` varchar(64) NULL,
    `last_rejection_at` datetime(6) NULL,
    PRIMARY KEY (`intent_id`),
    KEY `chat_first_intents_ready` (`user_external_uid`,`account_generation`,`delivery_state`,`created_at`),
    UNIQUE KEY `chat_first_intents_continuity` (`user_external_uid`,`account_generation`,`continuity_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `chat_first_deferrals` (
    `deferral_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `continuity_key` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `subject` json NOT NULL,
    `question` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `due_at` datetime(6) NOT NULL,
    `state` varchar(32) NOT NULL DEFAULT 'pending',
    `released_intent_id` varchar(128) NULL,
    PRIMARY KEY (`deferral_id`),
    UNIQUE KEY `chat_first_deferrals_continuity` (`user_external_uid`,`account_generation`,`continuity_key`),
    KEY `chat_first_deferrals_due` (`user_external_uid`,`account_generation`,`state`,`due_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `user_byok` (
    `user_external_uid` varchar(255) NOT NULL,
    `fingerprints` json NOT NULL,
    `active` boolean NOT NULL DEFAULT TRUE,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `staged_tasks` (
    `id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `description` varchar(5000) NOT NULL,
    `completed` boolean NOT NULL DEFAULT FALSE,
    `due_at` datetime(6) NULL,
    `source` varchar(128) NULL,
    `priority` varchar(32) NULL,
    `metadata` text NULL,
    `category` varchar(128) NULL,
    `relevance_score` int NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `staged_tasks_user_active` (`user_external_uid`,`completed`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `workstreams` (
    `id` varchar(255) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `goal_id` varchar(255) NULL,
    `title` varchar(256) NOT NULL, `objective` varchar(2048) NOT NULL, `status` varchar(32) NOT NULL DEFAULT 'open',
    `current_state_summary` varchar(4000) NOT NULL DEFAULT '', `next_review_at` datetime(6) NULL,
    `last_meaningful_progress_at` datetime(6) NULL, `latest_event_sequence` int NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL, `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `workstreams_user_updated` (`user_external_uid`,`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `workstream_events` (
    `event_id` varchar(255) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `workstream_id` varchar(255) NOT NULL,
    `sequence` int NOT NULL, `kind` varchar(64) NOT NULL, `summary` varchar(2000) NOT NULL, `evidence_refs` json NULL,
    `sensitivity` varchar(32) NOT NULL DEFAULT 'normal', `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`event_id`), UNIQUE KEY `workstream_event_sequence` (`workstream_id`,`sequence`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `workstream_artifacts` (
    `artifact_id` varchar(255) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `workstream_id` varchar(255) NOT NULL,
    `logical_key` varchar(256) NOT NULL, `version` int NOT NULL, `kind` varchar(64) NOT NULL, `uri` varchar(2048) NOT NULL,
    `content_hash` varchar(128) NOT NULL, `status` varchar(32) NOT NULL DEFAULT 'draft', `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`artifact_id`), KEY `workstream_artifact_key` (`workstream_id`,`logical_key`,`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `workstream_checkpoints` (
    `checkpoint_id` varchar(255) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `workstream_id` varchar(255) NOT NULL,
    `runtime_id` varchar(255) NOT NULL, `last_event_sequence` int NOT NULL, `context_summary` varchar(4000) NOT NULL,
    `evidence_refs` json NULL, `updated_at` datetime(6) NOT NULL, PRIMARY KEY (`checkpoint_id`),
    UNIQUE KEY `workstream_checkpoint_runtime` (`workstream_id`,`runtime_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `work_intent_receipts` (
    `receipt_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL DEFAULT 0,
    `idempotency_key` varchar(256) NOT NULL,
    `request_hash` char(36) NOT NULL,
    `workstream_id` varchar(255) NOT NULL,
    `task_id` varchar(255) NOT NULL,
    `goal_external_id` varchar(64) NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`receipt_id`), UNIQUE KEY `work_intent_idempotency` (`user_external_uid`,`account_generation`,`idempotency_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_goal_link_receipts` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL DEFAULT 0,
    `idempotency_key` varchar(256) NOT NULL,
    `request_hash` char(64) NOT NULL,
    `result` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `task_goal_link_idempotency` (`user_external_uid`,`account_generation`,`idempotency_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `candidates` (
    `candidate_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `subject_kind` varchar(32) NOT NULL,
    `proposed_action` varchar(32) NOT NULL,
    `task_id` varchar(255) NULL,
    `task_change` json NULL,
    `workstream_proposal` json NULL,
    `capture_confidence` decimal(5,4) NOT NULL,
    `ownership_confidence` decimal(5,4) NOT NULL,
    `goal_id` varchar(255) NULL,
    `workstream_id` varchar(255) NULL,
    `evidence_refs` json NOT NULL,
    `source_surface` varchar(64) NOT NULL,
    `compatibility` json NULL,
    `status` varchar(32) NOT NULL DEFAULT 'pending',
    `account_generation` bigint NOT NULL DEFAULT 0,
    `idempotency_key` varchar(512) NOT NULL,
    `resolution_reason` varchar(64) NULL,
    `result_task_id` varchar(255) NULL,
    `result_workstream_id` varchar(255) NULL,
    `created_at` datetime(6) NOT NULL,
    `resolved_at` datetime(6) NULL,
    `expires_at` datetime(6) NULL,
    PRIMARY KEY (`candidate_id`),
    UNIQUE KEY `candidates_idempotency` (`user_external_uid`,`account_generation`,`idempotency_key`),
    KEY `candidates_user_status` (`user_external_uid`,`account_generation`,`status`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `speech_profiles` (
    `user_external_uid` varchar(255) NOT NULL,
    `profile_url` varchar(2048) NULL,
    `duration_seconds` decimal(12,3) NOT NULL DEFAULT 0,
    `embedding` json NULL,
    `extra_samples` json NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `referral_claims` (
    `referred_uid` varchar(255) NOT NULL,
    `referrer_uid` varchar(255) NOT NULL,
    `program` varchar(128) NOT NULL,
    `claimed_at` datetime(6) NOT NULL,
    `trial_ends_at` datetime(6) NOT NULL,
    PRIMARY KEY (`referred_uid`), KEY `referral_claims_referrer` (`referrer_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mobile_feedback` (
    `user_external_uid` varchar(255) NOT NULL, `feedback_id` varchar(128) NOT NULL, `event_id` varchar(128) NOT NULL,
    `payload_hash` char(64) NOT NULL, `kind` varchar(64) NOT NULL, `target_kind` varchar(32) NOT NULL,
    `target_id` varchar(256) NOT NULL, `related_conversation_id` varchar(128) NULL, `value` tinyint NOT NULL, `reason` varchar(128) NULL, `comment` varchar(1000) NULL,
    `platform` varchar(32) NULL, `app_version` varchar(64) NULL, `app_build` varchar(64) NULL,
    `client_app_namespace` varchar(128) NULL, `client_app_profile` varchar(32) NULL, `correlation_id` varchar(128) NULL,
    `created_at` datetime(6) NOT NULL, PRIMARY KEY (`user_external_uid`,`feedback_id`), UNIQUE KEY `mobile_feedback_event` (`event_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `advice` (
    `id` varchar(64) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `content` text NOT NULL,
    `category` varchar(100) NOT NULL DEFAULT 'other', `reasoning` text NULL, `source_app` varchar(200) NULL,
    `confidence` double NOT NULL DEFAULT 0.5, `context_summary` text NULL, `current_activity` varchar(500) NULL,
    `is_read` boolean NOT NULL DEFAULT FALSE, `is_dismissed` boolean NOT NULL DEFAULT FALSE,
    `created_at` datetime(6) NOT NULL, `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `advice_uid_created` (`user_external_uid`,`created_at`), KEY `advice_uid_category` (`user_external_uid`,`category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `recording_sessions` (
    `recording_session_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `conversation_id` varchar(128) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`recording_session_id`), KEY `recording_sessions_uid` (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `frame_requests` (
    `request_id` varchar(128) NOT NULL, `user_external_uid` varchar(255) NOT NULL, `device_id` varchar(256) NOT NULL,
    `account_generation` bigint NOT NULL DEFAULT 0, `dedupe_key` varchar(256) NOT NULL, `dedupe_window` int NOT NULL DEFAULT 0,
    `attempt_number` int NOT NULL DEFAULT 0, `conversation_id` varchar(256) NULL, `screenshot_id` varchar(256) NULL,
    `state` varchar(32) NOT NULL DEFAULT 'requested', `created_at` datetime(6) NOT NULL, `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `expires_at` datetime(6) NOT NULL, `claimed_at` datetime(6) NULL, `uploaded_at` datetime(6) NULL, `attached_at` datetime(6) NULL,
    `terminal_reason` varchar(240) NULL, `byte_count` bigint NOT NULL DEFAULT 0, `content_type` varchar(100) NULL, `storage_id` varchar(256) NULL,
    `cleanup_state` varchar(32) NOT NULL DEFAULT 'not_required', `cleanup_attempts` int NOT NULL DEFAULT 0, `cleanup_next_attempt_at` datetime(6) NULL,
    PRIMARY KEY (`request_id`), KEY `frame_requests_owner_pending` (`user_external_uid`,`device_id`,`account_generation`,`state`,`expires_at`),
    KEY `frame_requests_dedupe` (`user_external_uid`,`device_id`,`account_generation`,`dedupe_key`,`dedupe_window`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `plugins_data` (
    `id` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL DEFAULT '',
    `uid` varchar(255) NULL,
    `private` boolean NOT NULL DEFAULT FALSE,
    `approved` boolean NOT NULL DEFAULT FALSE,
    `status` varchar(32) NOT NULL DEFAULT 'approved',
    `category` varchar(128) NOT NULL DEFAULT 'other',
    `author` varchar(255) NOT NULL DEFAULT '',
    `description` text NOT NULL,
    `image` text NOT NULL,
    `capabilities` json NULL,
    `external_integration` json NULL,
    `chat_tools` json NULL,
    `installs` int NOT NULL DEFAULT 0,
    `popular` boolean NOT NULL DEFAULT FALSE,
    `rating_avg` double NULL,
    `rating_count` int NOT NULL DEFAULT 0,
    `is_paid` boolean NOT NULL DEFAULT FALSE,
    `price` double NULL,
    `disabled` boolean NOT NULL DEFAULT FALSE,
    `disabled_reason` text NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    KEY `plugins_data_public` (`approved`, `private`),
    KEY `plugins_data_owner` (`uid`),
    KEY `plugins_data_category` (`category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE `plugins_data` ADD COLUMN IF NOT EXISTS `chat_tools` json NULL AFTER `external_integration`;

CREATE TABLE IF NOT EXISTS `user_enabled_apps` (
    `user_external_uid` varchar(255) NOT NULL,
    `app_id` varchar(255) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`, `app_id`),
    KEY `user_enabled_apps_app` (`app_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `app_reviews` (
    `app_id` varchar(255) NOT NULL,
    `reviewer_uid` varchar(255) NOT NULL,
    `score` double NOT NULL DEFAULT 0,
    `review` text NOT NULL,
    `username` varchar(255) NOT NULL DEFAULT '',
    `response` text NULL,
    `rated_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `responded_at` datetime(6) NULL,
    PRIMARY KEY (`app_id`, `reviewer_uid`),
    KEY `app_reviews_rated` (`app_id`, `rated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `app_api_keys` (
    `id` varchar(64) NOT NULL,
    `app_id` varchar(255) NOT NULL,
    `key_hash` char(64) NOT NULL,
    `label` varchar(255) NOT NULL DEFAULT '',
    `created_at` datetime(6) NOT NULL,
    `last_used_at` datetime(6) NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `app_api_keys_hash` (`key_hash`),
    KEY `app_api_keys_app` (`app_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `app_testers` (
    `uid` varchar(255) NOT NULL,
    `app_id` varchar(255) NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`uid`, `app_id`), KEY `app_testers_uid` (`uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `summary_app_ids` (
    `app_id` varchar(255) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`app_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mcp_api_keys` (
    `id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL,
    `key_prefix` varchar(64) NOT NULL,
    `hashed_key` char(64) NOT NULL,
    `app_id` varchar(255) NULL,
    `scopes` json NULL,
    `created_at` datetime(6) NOT NULL,
    `last_used_at` datetime(6) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `mcp_api_keys_hash` (`hashed_key`),
    KEY `mcp_api_keys_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mcp_oauth_grants` (
    `id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `client_id` varchar(255) NOT NULL,
    `client_name` varchar(255) NOT NULL DEFAULT '',
    `resource` varchar(2048) NOT NULL,
    `scopes` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `last_used_at` datetime(6) NULL,
    `revoked_at` datetime(6) NULL,
    PRIMARY KEY (`id`),
    KEY `mcp_oauth_grants_user` (`user_external_uid`, `created_at`),
    KEY `mcp_oauth_grants_client` (`client_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `dev_api_keys` (
    `id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL,
    `key_prefix` varchar(64) NOT NULL,
    `hashed_key` char(64) NOT NULL,
    `scopes` json NULL,
    `created_at` datetime(6) NOT NULL,
    `last_used_at` datetime(6) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `dev_api_keys_hash` (`hashed_key`),
    KEY `dev_api_keys_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mcp_oauth_authorization_codes` (
    `id` varchar(64) NOT NULL,
    `code_hash` char(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `client_id` varchar(255) NOT NULL,
    `redirect_uri` varchar(2048) NOT NULL,
    `resource` varchar(2048) NOT NULL,
    `scopes` json NOT NULL,
    `code_challenge` varchar(255) NOT NULL,
    `code_challenge_method` varchar(16) NOT NULL DEFAULT 'S256',
    `created_at` datetime(6) NOT NULL,
    `expires_at` datetime(6) NOT NULL,
    `used_at` datetime(6) NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `mcp_oauth_codes_hash` (`code_hash`),
    KEY `mcp_oauth_codes_user` (`user_external_uid`), KEY `mcp_oauth_codes_expiry` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mcp_oauth_access_tokens` (
    `id` varchar(64) NOT NULL,
    `token_hash` char(64) NOT NULL,
    `grant_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `client_id` varchar(255) NOT NULL,
    `resource` varchar(2048) NOT NULL,
    `scopes` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `expires_at` datetime(6) NOT NULL,
    `revoked_at` datetime(6) NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `mcp_oauth_access_hash` (`token_hash`),
    KEY `mcp_oauth_access_user` (`user_external_uid`), KEY `mcp_oauth_access_expiry` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `mcp_oauth_refresh_tokens` (
    `id` varchar(64) NOT NULL,
    `token_hash` char(64) NOT NULL,
    `grant_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `client_id` varchar(255) NOT NULL,
    `resource` varchar(2048) NOT NULL,
    `scopes` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `expires_at` datetime(6) NOT NULL,
    `used_at` datetime(6) NULL,
    `replaced_by` varchar(64) NULL,
    `revoked_at` datetime(6) NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `mcp_oauth_refresh_hash` (`token_hash`),
    KEY `mcp_oauth_refresh_grant` (`grant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `realtime_sessions` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `token_hash` char(64) NOT NULL,
    `provider` varchar(32) NOT NULL,
    `model` varchar(255) NOT NULL,
    `status` varchar(32) NOT NULL,
    `expires_at` varchar(64) NOT NULL,
    `max_minutes` int NOT NULL DEFAULT 30,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `realtime_sessions_token_hash` (`token_hash`), KEY `realtime_sessions_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `realtime_usage` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `turn_id` varchar(255) NULL,
    `provider` varchar(32) NOT NULL,
    `model` varchar(255) NOT NULL,
    `input_tokens` bigint NOT NULL DEFAULT 0,
    `output_tokens` bigint NOT NULL DEFAULT 0,
    `cached_tokens` bigint NOT NULL DEFAULT 0,
    `cost_micro_usd` bigint NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `realtime_usage_user_turn` (`user_external_uid`, `turn_id`), KEY `realtime_usage_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `llm_usage` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `allocation` varchar(64) NOT NULL,
    `feature` varchar(128) NOT NULL DEFAULT 'chat',
    `input_tokens` bigint NOT NULL DEFAULT 0,
    `output_tokens` bigint NOT NULL DEFAULT 0,
    `questions` int NOT NULL DEFAULT 0,
    `cost_micro_usd` bigint NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `llm_usage_user_created` (`user_external_uid`,`created_at`),
    KEY `llm_usage_user_allocation` (`user_external_uid`,`allocation`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE `llm_usage` ADD COLUMN IF NOT EXISTS `feature` varchar(128) NOT NULL DEFAULT 'chat';
ALTER TABLE `llm_usage` ADD COLUMN IF NOT EXISTS `input_tokens` bigint NOT NULL DEFAULT 0;
ALTER TABLE `llm_usage` ADD COLUMN IF NOT EXISTS `output_tokens` bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS `llm_proxy_attempts` (
    `request_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NULL,
    `caller` varchar(64) NOT NULL,
    `provider` varchar(64) NOT NULL,
    `model` varchar(128) NOT NULL,
    `api_surface` varchar(128) NOT NULL,
    `payer` varchar(32) NOT NULL,
    `outcome` varchar(64) NOT NULL,
    `upstream_status` int NOT NULL DEFAULT 0,
    `input_bytes` bigint NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`request_id`),
    KEY `llm_proxy_attempts_user_created` (`user_external_uid`,`created_at`),
    KEY `llm_proxy_attempts_provider_created` (`provider`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `trend_categories` (
    `id` varchar(128) NOT NULL,
    `category` varchar(64) NOT NULL,
    `type` varchar(32) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `trend_categories_category` (`category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `trend_topics` (
    `id` varchar(128) NOT NULL,
    `category_id` varchar(128) NOT NULL,
    `topic` varchar(255) NOT NULL,
    `memory_ids` json NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `trend_topics_category` (`category_id`),
    CONSTRAINT `trend_topics_category_fk` FOREIGN KEY (`category_id`) REFERENCES `trend_categories` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `daily_summaries` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `summary_date` date NOT NULL,
    `visibility` varchar(16) NOT NULL DEFAULT 'private',
    `payload` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `daily_summaries_external_id` (`external_id`), KEY `daily_summaries_user_date` (`user_external_uid`, `summary_date`), KEY `daily_summaries_visibility` (`visibility`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `focus_sessions` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `status` enum('focused','distracted') NOT NULL,
    `app_or_site` varchar(500) NOT NULL,
    `description` varchar(5000) NOT NULL,
    `message` varchar(5000) NULL,
    `duration_seconds` bigint NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `focus_sessions_external_id` (`external_id`), KEY `focus_sessions_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `screen_activity` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `storage_id` varchar(512) NOT NULL,
    `local_screenshot_id` varchar(255) NOT NULL,
    `timestamp` varchar(32) NOT NULL,
    `app_name` varchar(512) NOT NULL DEFAULT '',
    `window_title` varchar(2048) NOT NULL DEFAULT '',
    `ocr_text` varchar(8192) NOT NULL DEFAULT '',
    `device_name` varchar(255) NULL,
    `client_device_id` varchar(255) NULL,
    `capture_eligible` boolean NOT NULL DEFAULT false,
    `account_generation` int NOT NULL DEFAULT 0,
    `device_retention_seconds` int NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `screen_activity_user_storage` (`user_external_uid`, `storage_id`), KEY `screen_activity_user_timestamp` (`user_external_uid`, `timestamp`), KEY `screen_activity_user_app_timestamp` (`user_external_uid`, `app_name`, `timestamp`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `people` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `name` varchar(40) NOT NULL,
    `speech_samples` json NOT NULL,
    `speech_sample_transcripts` json NULL,
    `speech_samples_version` int NOT NULL DEFAULT 3,
    `speaker_embedding` json NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `people_external_id` (`external_id`), UNIQUE KEY `people_user_name` (`user_external_uid`, `name`), KEY `people_user_created` (`user_external_uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_daily_usage` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `usage_date` date NOT NULL,
    `timezone_name` varchar(128) NOT NULL,
    `client_device_id` varchar(200) NOT NULL,
    `watching_seconds` bigint NOT NULL DEFAULT 0,
    `listening_seconds` bigint NOT NULL DEFAULT 0,
    `proactive_cards_shown` bigint NOT NULL DEFAULT 0,
    `proactive_cards_acted` bigint NOT NULL DEFAULT 0,
    `ptt_turns` bigint NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `desktop_daily_usage_user_date_device` (`user_external_uid`, `usage_date`, `client_device_id`), KEY `desktop_daily_usage_user_date` (`user_external_uid`, `usage_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `notification_tokens` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `token` varchar(2048) NOT NULL,
    `platform` varchar(64) NOT NULL DEFAULT 'unknown',
    `device_key` varchar(255) NOT NULL DEFAULT 'default',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `notification_tokens_user_device` (`user_id`, `device_key`),
    KEY `notification_tokens_token` (`token`(255)),
    CONSTRAINT `notification_tokens_users_tokens` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `devices` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `device_id` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL DEFAULT 'Remi Wearable',
    `firmware_version` varchar(255) NOT NULL DEFAULT '',
    `battery_level` int NOT NULL DEFAULT -1,
    `last_seen_at` datetime(6) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `devices_device_id` (`device_id`),
    KEY `devices_user_id` (`user_id`),
    CONSTRAINT `devices_users_devices` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `folders` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `name` varchar(100) NOT NULL,
    `description` varchar(500) NULL,
    `color` varchar(32) NOT NULL DEFAULT '#6B7280',
    `icon` varchar(64) NOT NULL DEFAULT 'folder',
    `order` int NOT NULL DEFAULT 0,
    `is_default` boolean NOT NULL DEFAULT false,
    `is_system` boolean NOT NULL DEFAULT false,
    `category_mapping` varchar(64) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `folders_external_id` (`external_id`),
    KEY `folders_user_order` (`user_id`, `order`),
    CONSTRAINT `folders_users_folders` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `goals` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `title` varchar(500) NOT NULL,
    `desired_outcome` varchar(2000) NOT NULL DEFAULT '',
    `why_it_matters` varchar(2000) NULL,
    `success_criteria` json NULL,
    `horizon_at` datetime(6) NULL,
    `status` enum('background','focused','paused','achieved','abandoned') NOT NULL DEFAULT 'background',
    `focus_rank` int NULL,
    `metric` json NULL,
    `source` enum('user','ai_suggested','imported') NOT NULL DEFAULT 'user',
    `ended_at` datetime(6) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `goals_external_id` (`external_id`), KEY `goals_user_status` (`user_id`,`status`),
    CONSTRAINT `goals_users_goals` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `goal_progress_events` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `sequence` int NOT NULL,
    `kind` enum('evidence','metric_update','milestone','status_change') NOT NULL,
    `summary` varchar(1000) NOT NULL,
    `evidence_refs` json NULL,
    `metric` json NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `goal_id` bigint NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `goal_progress_events_external_id` (`external_id`), UNIQUE KEY `goal_progress_events_goal_sequence` (`goal_id`,`sequence`),
    CONSTRAINT `goal_progress_events_goals_events` FOREIGN KEY (`goal_id`) REFERENCES `goals` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `conversations` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `title` varchar(255) NOT NULL DEFAULT '',
    `summary` text NOT NULL,
    `visibility` varchar(32) NOT NULL DEFAULT 'private',
    `starred` boolean NOT NULL DEFAULT false,
    `started_at` datetime(6) NOT NULL,
    `ended_at` datetime(6) NULL,
    `status` enum('in_progress','processing','merging','completed','failed') NOT NULL DEFAULT 'in_progress',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    `device_id` bigint NULL,
    `folder_id` bigint NULL,
    `audio_files` json NULL,
    `conversation_audio` json NULL,
    `structured` json NULL,
    `calendar_event` json NULL,
    `photos` json NULL,
    `screenshot_sharing_enabled` boolean NOT NULL DEFAULT FALSE,
    `screen_frames_revision` bigint NOT NULL DEFAULT 0,
    `screen_frames_adjudicated_at` datetime(6) NULL,
    `data_protection_level` varchar(32) NOT NULL DEFAULT 'standard',
    PRIMARY KEY (`id`),
    KEY `conversations_user_id` (`user_id`),
    KEY `conversations_device_id` (`device_id`),
    CONSTRAINT `conversations_users_conversations` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL,
    CONSTRAINT `conversations_devices_conversations` FOREIGN KEY (`device_id`) REFERENCES `devices` (`id`) ON DELETE SET NULL,
    CONSTRAINT `conversations_folders_conversations` FOREIGN KEY (`folder_id`) REFERENCES `folders` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `screen_frame_adjudication_attempts` (
    `user_external_uid` varchar(255) NOT NULL,
    `purpose` varchar(64) NOT NULL,
    `attempt_id` char(36) NOT NULL,
    `fingerprint` char(64) NOT NULL,
    `response` json NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`purpose`,`attempt_id`),
    KEY `screen_frame_attempts_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `sync_jobs` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `job_id` varchar(128) NOT NULL,
    `uid` varchar(255) NOT NULL,
    `conversation_id` varchar(128) NULL,
    `status` varchar(32) NOT NULL DEFAULT 'queued',
    `total_segments` int NOT NULL DEFAULT 0,
    `processed_segments` int NOT NULL DEFAULT 0,
    `successful_segments` int NOT NULL DEFAULT 0,
    `failed_segments` int NOT NULL DEFAULT 0,
    `lane` varchar(32) NOT NULL DEFAULT 'fresh',
    `reason_code` varchar(128) NULL,
    `retry_after` int NULL,
    `recording_age_seconds` int NULL,
    `error` varchar(255) NULL,
    `result` json NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `sync_jobs_job_id` (`job_id`),
    KEY `sync_jobs_uid_created_at` (`uid`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `calendar_meetings` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `calendar_event_id` varchar(1024) NOT NULL,
    `calendar_source` varchar(64) NOT NULL DEFAULT 'system_calendar',
    `title` varchar(1000) NOT NULL,
    `participants` json NULL,
    `platform` varchar(255) NULL,
    `meeting_link` varchar(2048) NULL,
    `start_time` datetime(6) NOT NULL,
    `end_time` datetime(6) NOT NULL,
    `duration_minutes` int NOT NULL,
    `notes` text NULL,
    `synced_at` datetime(6) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `calendar_meetings_external_id` (`external_id`),
    KEY `calendar_meetings_user_start` (`user_id`, `start_time`),
    KEY `calendar_meetings_user_event_source` (`user_id`, `calendar_event_id`(255), `calendar_source`),
    CONSTRAINT `calendar_meetings_users_meetings` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `csat_ratings` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `platform` varchar(32) NOT NULL,
    `app_version` varchar(32) NOT NULL DEFAULT '',
    `score` int NOT NULL,
    `comment` text NOT NULL,
    `revision` int NOT NULL DEFAULT 0,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `csat_ratings_external_id` (`external_id`),
    UNIQUE KEY `csat_ratings_user_platform` (`user_id`, `platform`),
    CONSTRAINT `csat_ratings_users_ratings` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `chat_files` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `name` varchar(1024) NOT NULL,
    `thumbnail` varchar(2048) NOT NULL DEFAULT '',
    `mime_type` varchar(255) NOT NULL,
    `openai_file_id` varchar(255) NOT NULL,
    `thumb_name` varchar(1024) NOT NULL DEFAULT '',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `chat_files_external_id` (`external_id`),
    KEY `chat_files_user_created_at` (`user_id`, `created_at`),
    CONSTRAINT `chat_files_users_files` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `transcript_segments` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `speaker` varchar(255) NOT NULL DEFAULT 'unknown',
    `speaker_id` int NOT NULL DEFAULT 0,
    `is_user` boolean NOT NULL DEFAULT false,
    `person_id` varchar(255) NULL,
    `text` text NOT NULL,
    `start_ms` bigint NOT NULL,
    `end_ms` bigint NOT NULL,
    `source` varchar(255) NOT NULL DEFAULT 'stt',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `conversation_id` bigint NULL,
    `workstream_id` varchar(255) NULL,
    `goal_external_id` varchar(64) NULL,
    PRIMARY KEY (`id`),
    KEY `transcript_segments_conversation_id` (`conversation_id`),
    CONSTRAINT `transcript_segments_conversations_segments` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memories` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `type` enum('fact','decision','preference','event') NOT NULL DEFAULT 'fact',
    `category` varchar(64) NOT NULL DEFAULT 'interesting',
    `visibility` varchar(32) NOT NULL DEFAULT 'private',
    `tags` json NULL,
    `is_read` boolean NOT NULL DEFAULT false,
    `is_dismissed` boolean NOT NULL DEFAULT false,
    `user_review` boolean NULL,
    `is_baseline` boolean NOT NULL DEFAULT false,
    `item_revision` bigint NOT NULL DEFAULT 1,
    `curation_weight` int NOT NULL DEFAULT 0,
    `memory_use` json NULL,
    `data_protection_level` varchar(32) NOT NULL DEFAULT 'standard',
    `content` text NOT NULL,
    `importance` int NOT NULL DEFAULT 50,
    `event_time` datetime(6) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    `conversation_id` bigint NULL,
    PRIMARY KEY (`id`),
    KEY `memories_user_id` (`user_id`),
    KEY `memories_conversation_id` (`conversation_id`),
    CONSTRAINT `memories_users_memories` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL,
    CONSTRAINT `memories_conversations_memories` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memory_mutation_journal` (
    `journal_id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `memory_id` bigint NOT NULL,
    `mutation_kind` varchar(128) NOT NULL,
    `operation_id` varchar(128) NULL,
    `feedback_id` varchar(128) NULL,
    `action` varchar(32) NULL,
    `expected_revision` bigint NULL,
    `before_state` json NULL,
    `after_state` json NULL,
    `result` json NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`journal_id`),
    UNIQUE KEY `memory_mutation_operation` (`user_external_uid`,`operation_id`),
    UNIQUE KEY `memory_mutation_feedback` (`user_external_uid`,`feedback_id`),
    KEY `memory_mutation_memory` (`user_external_uid`,`memory_id`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memory_review_conflicts` (
    `review_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `memory_id` bigint NULL,
    `payload` json NOT NULL,
    `status` varchar(32) NOT NULL DEFAULT 'pending',
    `resolution` json NULL,
    `created_at` datetime(6) NOT NULL,
    `resolved_at` datetime(6) NULL,
    PRIMARY KEY (`review_id`),
    KEY `memory_reviews_user_status` (`user_external_uid`,`status`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memory_import_runs` (
    `run_id` varchar(160) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `source_type` varchar(128) NOT NULL,
    `source_account_hash` varchar(255) NULL,
    `importer_version` varchar(64) NOT NULL,
    `extractor_version` varchar(64) NULL,
    `status` varchar(32) NOT NULL DEFAULT 'received',
    `artifact_count` int NOT NULL DEFAULT 0,
    `deduped_count` int NOT NULL DEFAULT 0,
    `candidate_count` int NOT NULL DEFAULT 0,
    `accepted_count` int NOT NULL DEFAULT 0,
    `promoted_count` int NOT NULL DEFAULT 0,
    `started_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `completed_at` datetime(6) NULL,
    `last_error` text NULL,
    PRIMARY KEY (`run_id`),
    KEY `memory_import_runs_user_updated` (`user_external_uid`,`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memory_import_artifacts` (
    `artifact_id` varchar(160) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `run_id` varchar(160) NOT NULL,
    `source_type` varchar(128) NOT NULL,
    `external_id` varchar(512) NULL,
    `content_hash` varchar(128) NOT NULL,
    `title` varchar(2048) NULL,
    `snippet` text NULL,
    `redacted_body` mediumtext NULL,
    `metadata` json NOT NULL,
    `occurred_at` datetime(6) NULL,
    `captured_at` datetime(6) NOT NULL,
    `client_device_id` varchar(255) NULL,
    `source_state` varchar(32) NOT NULL DEFAULT 'active',
    `redaction_status` varchar(64) NOT NULL DEFAULT 'title_snippet_only',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`artifact_id`),
    KEY `memory_import_artifacts_user_source` (`user_external_uid`,`source_type`,`created_at`),
    KEY `memory_import_artifacts_run` (`run_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `x_posts` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `user_external_uid` varchar(255) NOT NULL,
    `post_id` varchar(255) NOT NULL,
    `kind` varchar(32) NOT NULL DEFAULT 'tweet',
    `text` text NOT NULL,
    `created_at` datetime(6) NULL,
    `payload` json NOT NULL,
    `memory_extraction_status` varchar(32) NOT NULL DEFAULT 'pending',
    `memory_extracted_at` datetime(6) NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `x_posts_user_post` (`user_external_uid`,`post_id`),
    KEY `x_posts_user_created` (`user_external_uid`,`created_at`),
    KEY `x_posts_user_kind` (`user_external_uid`,`kind`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `conversation_ingest_sessions` (
    `user_external_uid` varchar(255) NOT NULL,
    `client_session_id` varchar(200) NOT NULL,
    `conversation_id` varchar(255) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`client_session_id`),
    UNIQUE KEY `conversation_ingest_conversation` (`conversation_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `todos` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `title` varchar(255) NOT NULL,
    `description` text NOT NULL,
    `due_at` datetime(6) NULL,
    `status` enum('open','completed','cancelled') NOT NULL DEFAULT 'open',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    `conversation_id` bigint NULL,
    PRIMARY KEY (`id`),
    KEY `todos_user_id` (`user_id`),
    KEY `todos_conversation_id` (`conversation_id`),
    CONSTRAINT `todos_users_todos` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL,
    CONSTRAINT `todos_conversations_todos` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `action_items` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `description` varchar(4096) NOT NULL,
    `status` enum('active','completed','cancelled','superseded') NOT NULL DEFAULT 'active',
    `owner` enum('user','other','unknown') NOT NULL DEFAULT 'user',
    `priority` enum('high','medium','low') NULL,
    `due_confidence` double NULL,
    `source` varchar(64) NOT NULL DEFAULT 'manual',
    `provenance` json NULL,
    `sort_order` int NOT NULL DEFAULT 0,
    `indent_level` int NOT NULL DEFAULT 0,
    `recurrence_rule` varchar(128) NULL,
    `recurrence_parent_id` bigint NULL,
    `due_at` datetime(6) NULL,
    `completed_at` datetime(6) NULL,
    `superseded_by` bigint NULL,
    `is_locked` boolean NOT NULL DEFAULT false,
    `exported` boolean NOT NULL DEFAULT false,
    `export_date` datetime(6) NULL,
    `export_platform` varchar(64) NULL,
    `apple_reminder_id` varchar(512) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    `conversation_id` bigint NULL,
    PRIMARY KEY (`id`),
    KEY `action_items_user_id` (`user_id`),
    KEY `action_items_conversation_id` (`conversation_id`),
    CONSTRAINT `action_items_users_action_items` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL,
    CONSTRAINT `action_items_conversations_action_items` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `task_intelligence_control` (
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL DEFAULT 0,
    `workflow_mode` varchar(32) NOT NULL DEFAULT 'live',
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_recommendation_projections` (
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `evaluation_id` varchar(128) NOT NULL,
    `device_scope` varchar(128) NOT NULL DEFAULT '',
    `payload` json NOT NULL,
    `generated_at` datetime(6) NOT NULL,
    `expires_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`account_generation`,`device_scope`),
    KEY `task_projection_expiry` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_interventions` (
    `intervention_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `idempotency_key` varchar(512) NOT NULL,
    `request_hash` char(64) NOT NULL,
    `attribution_chain_id` varchar(128) NOT NULL,
    `surface` varchar(32) NOT NULL,
    `subject_kind` varchar(32) NOT NULL,
    `subject_id` varchar(128) NOT NULL,
    `dedupe_key` varchar(128) NOT NULL,
    `evidence_refs` json NULL,
    `expires_at` datetime(6) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`intervention_id`), UNIQUE KEY `task_intervention_idem` (`user_external_uid`,`account_generation`,`idempotency_key`),
    KEY `task_intervention_chain` (`user_external_uid`,`account_generation`,`attribution_chain_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_feedback` (
    `feedback_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `idempotency_key` varchar(512) NOT NULL,
    `request_hash` char(64) NOT NULL,
    `attribution_chain_id` varchar(128) NOT NULL,
    `payload` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`feedback_id`), UNIQUE KEY `task_feedback_idem` (`user_external_uid`,`account_generation`,`idempotency_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_outcomes` (
    `outcome_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `idempotency_key` varchar(512) NOT NULL,
    `request_hash` char(64) NOT NULL,
    `attribution_chain_id` varchar(128) NOT NULL,
    `payload` json NOT NULL,
    `occurred_at` datetime(6) NOT NULL,
    PRIMARY KEY (`outcome_id`), UNIQUE KEY `task_outcome_idem` (`user_external_uid`,`account_generation`,`idempotency_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `task_intelligence_snapshots` (
    `snapshot_id` varchar(128) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `account_generation` bigint NOT NULL,
    `snapshot_kind` varchar(32) NOT NULL,
    `idempotency_key` varchar(512) NOT NULL,
    `request_hash` char(64) NOT NULL,
    `payload` json NOT NULL,
    `generated_at` datetime(6) NOT NULL,
    `expires_at` datetime(6) NOT NULL,
    PRIMARY KEY (`snapshot_id`), UNIQUE KEY `task_snapshot_idem` (`user_external_uid`,`account_generation`,`snapshot_kind`,`idempotency_key`),
    KEY `task_snapshot_current` (`user_external_uid`,`account_generation`,`snapshot_kind`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS `integration_notification_events` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `app_id` varchar(255) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `message` text NOT NULL,
    `source` varchar(128) NOT NULL,
    `metadata` json NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `notification_rate_limit` (`app_id`,`user_external_uid`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `chat_messages` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `text` text NOT NULL,
    `sender` enum('human','ai') NOT NULL,
    `type` enum('text','day_summary') NOT NULL DEFAULT 'text',
    `app_id` varchar(255) NULL,
    `chat_session_id` varchar(255) NULL,
    `rating` int NULL,
    `reported` boolean NOT NULL DEFAULT false,
    `report_reason` varchar(255) NULL,
    `data_protection_level` varchar(32) NOT NULL DEFAULT 'standard',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `chat_messages_external_id` (`external_id`),
    KEY `chat_messages_user_id` (`user_id`),
    CONSTRAINT `chat_messages_users_chat_messages` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `chat_sessions` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `external_id` varchar(64) NOT NULL,
    `title` varchar(500) NOT NULL DEFAULT 'New Chat',
    `app_id` varchar(255) NULL,
    `starred` boolean NOT NULL DEFAULT false,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `chat_sessions_external_id` (`external_id`),
    KEY `chat_sessions_user_id_updated_at` (`user_id`, `updated_at`),
    CONSTRAINT `chat_sessions_users_chat_sessions` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_releases` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `version` varchar(255) NOT NULL,
    `build_number` int NOT NULL,
    `download_url` varchar(2048) NOT NULL,
    `manual_download_url` varchar(2048) NULL,
    `ed_signature` varchar(1024) NOT NULL,
    `published_at` varchar(64) NOT NULL,
    `changelog` json NOT NULL,
    `is_live` boolean NOT NULL DEFAULT false,
    `is_critical` boolean NOT NULL DEFAULT false,
    `channel` varchar(32) NOT NULL DEFAULT 'staging',
    `platform` varchar(32) NOT NULL DEFAULT 'macos',
    `installer_url` varchar(2048) NULL,
    `feed_url` varchar(2048) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    KEY `desktop_releases_build_number` (`build_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_beta_control` (
    `id` tinyint NOT NULL,
    `promotion_enabled` boolean NOT NULL DEFAULT FALSE,
    `reserved_tag` varchar(128) NULL,
    `reserved_build_number` int NULL,
    `generation` bigint NOT NULL DEFAULT 0,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_release_manifests` (
    `release_id` varchar(255) NOT NULL,
    `platform` varchar(32) NOT NULL,
    `channel` varchar(32) NOT NULL,
    `version` varchar(255) NOT NULL,
    `build_number` int NOT NULL,
    `manifest` json NOT NULL,
    `manifest_sha256` char(64) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`release_id`),
    KEY `desktop_release_manifests_channel_build` (`platform`,`channel`,`build_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_channel_pointers` (
    `platform` varchar(32) NOT NULL,
    `channel` varchar(32) NOT NULL,
    `release_id` varchar(255) NOT NULL,
    `generation` bigint NOT NULL DEFAULT 0,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`platform`,`channel`),
    KEY `desktop_channel_pointers_release` (`release_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_preview_manifests` (
    `slug` varchar(63) NOT NULL,
    `source_sha` char(40) NOT NULL,
    `manifest` json NOT NULL,
    `manifest_sha256` char(64) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`slug`,`source_sha`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `desktop_preview_pointers` (
    `slug` varchar(63) NOT NULL,
    `source_sha` char(40) NOT NULL,
    `generation` bigint NOT NULL DEFAULT 0,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`slug`),
    KEY `desktop_preview_pointers_manifest` (`slug`,`source_sha`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `announcements` (
    `id` varchar(255) NOT NULL,
    `type` varchar(32) NOT NULL,
    `active` boolean NOT NULL DEFAULT true,
    `app_version` varchar(128) NULL,
    `firmware_version` varchar(128) NULL,
    `device_models` json NULL,
    `expires_at` datetime(6) NULL,
    `targeting` json NULL,
    `display` json NULL,
    `content` json NOT NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `announcements_active_created` (`active`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `subscriptions` (
    `user_external_uid` varchar(255) NOT NULL,
    `plan` varchar(64) NOT NULL DEFAULT 'basic',
    `status` varchar(32) NOT NULL DEFAULT 'active',
    `stripe_subscription_id` varchar(255) NULL,
    `stripe_customer_id` varchar(255) NULL,
    `current_period_start` bigint NULL,
    `current_period_end` bigint NULL,
    `cancel_at_period_end` boolean NOT NULL DEFAULT false,
    `current_price_id` varchar(255) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `import_jobs` (
    `id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `source` varchar(32) NOT NULL,
    `status` varchar(32) NOT NULL,
    `total_files` int NULL,
    `processed_files` int NULL,
    `conversations_created` int NULL,
    `conversations_skipped` int NULL,
    `error` text NULL,
    `started_at` datetime(6) NULL,
    `completed_at` datetime(6) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), KEY `import_jobs_user_created` (`user_external_uid`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `knowledge_graphs` (
    `user_external_uid` varchar(255) NOT NULL,
    `graph_json` json NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `phone_numbers` (
    `id` varchar(64) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `phone_number` varchar(32) NOT NULL,
    `friendly_name` varchar(255) NULL,
    `twilio_sid` varchar(128) NULL,
    `verified_at` datetime(6) NOT NULL,
    `is_primary` boolean NOT NULL DEFAULT false,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`), UNIQUE KEY `phone_numbers_user_number` (`user_external_uid`,`phone_number`), KEY `phone_numbers_user_primary` (`user_external_uid`,`is_primary`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `phone_verifications` (
    `phone_number` varchar(32) NOT NULL,
    `user_external_uid` varchar(255) NOT NULL,
    `verification_sid` varchar(128) NULL,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`phone_number`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `phone_call_usage` (
    `user_external_uid` varchar(255) NOT NULL,
    `month_key` char(7) NOT NULL,
    `used` int NOT NULL DEFAULT 0,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`month_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `payment_methods` (
    `user_external_uid` varchar(255) NOT NULL,
    `method` varchar(32) NOT NULL,
    `email` varchar(320) NULL,
    `paypalme_url` varchar(1024) NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`method`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `payment_defaults` (
    `user_external_uid` varchar(255) NOT NULL,
    `default_method` varchar(32) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `stripe_connect_accounts` (
    `user_external_uid` varchar(255) NOT NULL,
    `account_id` varchar(255) NOT NULL,
    `country` char(2) NOT NULL,
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`), UNIQUE KEY `stripe_connect_account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `stripe_webhook_events` (
    `event_id` varchar(255) NOT NULL,
    `event_type` varchar(128) NOT NULL,
    `received_at` datetime(6) NOT NULL,
    PRIMARY KEY (`event_id`), KEY `stripe_webhook_events_received` (`received_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `announcement_dismissals` (
    `user_external_uid` varchar(255) NOT NULL,
    `announcement_id` varchar(255) NOT NULL,
    `cta_clicked` boolean NOT NULL DEFAULT false,
    `created_at` datetime(6) NOT NULL,
    PRIMARY KEY (`user_external_uid`,`announcement_id`),
    CONSTRAINT `announcement_dismissals_announcement` FOREIGN KEY (`announcement_id`) REFERENCES `announcements` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS desktop_prompts (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  external_id VARCHAR(191) NOT NULL UNIQUE,
  prompt_type VARCHAR(32) NOT NULL,
  question TEXT NOT NULL,
  options JSON NOT NULL,
  cta_label VARCHAR(255) NULL,
  cta_url TEXT NULL,
  trigger_kind VARCHAR(64) NOT NULL DEFAULT 'app_launch',
  trigger_count INT NOT NULL DEFAULT 0,
  max_per_day INT NOT NULL DEFAULT 1,
  audience JSON NOT NULL,
  active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  INDEX idx_desktop_prompts_active_id (active, external_id)
);

CREATE TABLE IF NOT EXISTS `fair_use_state` (
    `user_external_uid` varchar(255) NOT NULL,
    `stage` varchar(32) NOT NULL DEFAULT 'none',
    `case_ref` varchar(128) NOT NULL DEFAULT '',
    `daily_speech_ms` bigint NOT NULL DEFAULT 0,
    `three_day_speech_ms` bigint NOT NULL DEFAULT 0,
    `weekly_speech_ms` bigint NOT NULL DEFAULT 0,
    `dg_daily_limit_ms` bigint NOT NULL DEFAULT 0,
    `dg_used_ms` bigint NOT NULL DEFAULT 0,
    `dg_resets_at` datetime(6) NULL,
    `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`user_external_uid`),
    CONSTRAINT `fair_use_state_user_fk` FOREIGN KEY (`user_external_uid`) REFERENCES `users` (`external_uid`) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS `wrapped` (
    `user_external_uid` varchar(255) NOT NULL,
    `year` int NOT NULL,
    `status` varchar(32) NOT NULL DEFAULT 'not_generated',
    `result` json NULL,
    `error` text NULL,
    `progress` json NULL,
    `updated_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`user_external_uid`,`year`),
    CONSTRAINT `wrapped_user_fk` FOREIGN KEY (`user_external_uid`) REFERENCES `users` (`external_uid`) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS `fair_use_events` (
    `user_external_uid` varchar(255) NOT NULL,
    `event_id` varchar(128) NOT NULL,
    `case_ref` varchar(128) NOT NULL,
    `stage` varchar(32) NOT NULL,
    `notes` text NULL,
    `resolved_by` varchar(128) NULL,
    `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `resolved_at` datetime(6) NULL,
    PRIMARY KEY (`user_external_uid`, `event_id`),
    KEY `fair_use_events_case_ref` (`case_ref`),
    CONSTRAINT `fair_use_events_user_fk` FOREIGN KEY (`user_external_uid`) REFERENCES `users` (`external_uid`) ON DELETE CASCADE
);
