-- Remi initial relational schema for MySQL 8.
-- Keep this migration aligned with ent/schema. Ent remains the source of
-- application models; this checked-in SQL is useful for deployment and review.

CREATE TABLE IF NOT EXISTS `users` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `email` varchar(255) NOT NULL,
    `name` varchar(255) NOT NULL DEFAULT '',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `users_email` (`email`)
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

CREATE TABLE IF NOT EXISTS `conversations` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `title` varchar(255) NOT NULL DEFAULT '',
    `summary` text NOT NULL,
    `started_at` datetime(6) NOT NULL,
    `ended_at` datetime(6) NULL,
    `status` enum('in_progress','completed','failed') NOT NULL DEFAULT 'in_progress',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `user_id` bigint NULL,
    `device_id` bigint NULL,
    PRIMARY KEY (`id`),
    KEY `conversations_user_id` (`user_id`),
    KEY `conversations_device_id` (`device_id`),
    CONSTRAINT `conversations_users_conversations` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL,
    CONSTRAINT `conversations_devices_conversations` FOREIGN KEY (`device_id`) REFERENCES `devices` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `transcript_segments` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `speaker` varchar(255) NOT NULL DEFAULT 'unknown',
    `text` text NOT NULL,
    `start_ms` bigint NOT NULL,
    `end_ms` bigint NOT NULL,
    `source` varchar(255) NOT NULL DEFAULT 'stt',
    `created_at` datetime(6) NOT NULL,
    `updated_at` datetime(6) NOT NULL,
    `conversation_id` bigint NULL,
    PRIMARY KEY (`id`),
    KEY `transcript_segments_conversation_id` (`conversation_id`),
    CONSTRAINT `transcript_segments_conversations_segments` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `memories` (
    `id` bigint NOT NULL AUTO_INCREMENT,
    `type` enum('fact','decision','preference','event') NOT NULL DEFAULT 'fact',
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
