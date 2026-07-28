package store

const schemaV1 = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL COLLATE NOCASE UNIQUE,
	password_hash TEXT NOT NULL,
	display_name TEXT NOT NULL,
	preferred_model TEXT,
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash BLOB NOT NULL UNIQUE,
	expires_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	user_agent_hash BLOB
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS conversations (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title TEXT NOT NULL DEFAULT 'New chat',
	model TEXT NOT NULL,
	reasoning_effort TEXT NOT NULL DEFAULT 'auto',
	archived_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversations_user_updated ON conversations(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS messages (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
	model TEXT,
	reasoning_effort_requested TEXT,
	reasoning_effort_sent TEXT,
	status TEXT NOT NULL CHECK (status IN ('pending', 'streaming', 'completed', 'interrupted', 'error')),
	parent_message_id TEXT REFERENCES messages(id),
	provider_response_id TEXT,
	client_request_id TEXT,
	input_tokens INTEGER,
	output_tokens INTEGER,
	reasoning_tokens INTEGER,
	error_code TEXT,
	created_at INTEGER NOT NULL,
	completed_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_messages_conversation_created ON messages(conversation_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_user_request ON messages(user_id, client_request_id) WHERE client_request_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS message_parts (
	id TEXT PRIMARY KEY,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	sequence INTEGER NOT NULL,
	type TEXT NOT NULL,
	text_content TEXT,
	json_content TEXT,
	attachment_id TEXT,
	created_at INTEGER NOT NULL,
	UNIQUE(message_id, sequence)
);

CREATE TABLE IF NOT EXISTS attachments (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	conversation_id TEXT REFERENCES conversations(id) ON DELETE SET NULL,
	message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
	kind TEXT NOT NULL CHECK (kind IN ('upload', 'generated')),
	original_name TEXT,
	media_type TEXT NOT NULL,
	byte_size INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	storage_path TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	deleted_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_attachments_user ON attachments(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS provider_items (
	id TEXT PRIMARY KEY,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	sequence INTEGER NOT NULL,
	item_type TEXT NOT NULL,
	replay_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	UNIQUE(message_id, sequence)
);

CREATE TABLE IF NOT EXISTS context_checkpoints (
	id TEXT PRIMARY KEY,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	boundary_message_id TEXT NOT NULL REFERENCES messages(id),
	previous_checkpoint_id TEXT REFERENCES context_checkpoints(id),
	model TEXT NOT NULL,
	summary_text TEXT NOT NULL,
	source_first_message_id TEXT NOT NULL REFERENCES messages(id),
	source_last_message_id TEXT NOT NULL REFERENCES messages(id),
	estimated_tokens_before INTEGER NOT NULL,
	estimated_tokens_after INTEGER NOT NULL,
	source_bytes INTEGER NOT NULL,
	status TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checkpoints_conversation ON context_checkpoints(conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS tool_events (
	id TEXT PRIMARY KEY,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	call_id TEXT,
	tool_type TEXT NOT NULL,
	status TEXT NOT NULL,
	safe_arguments_json TEXT,
	safe_result_json TEXT,
	started_at INTEGER NOT NULL,
	completed_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_tool_events_message ON tool_events(message_id, started_at);
`

const schemaV2 = `
ALTER TABLE context_checkpoints ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE context_checkpoints ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
DELETE FROM context_checkpoints
WHERE rowid NOT IN (
	SELECT MAX(rowid)
	FROM context_checkpoints
	GROUP BY conversation_id, boundary_message_id
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_checkpoints_boundary
ON context_checkpoints(conversation_id, boundary_message_id);
`

const schemaV3 = `
ALTER TABLE users
ADD COLUMN role TEXT NOT NULL DEFAULT 'user'
CHECK (role IN ('user', 'admin'));

ALTER TABLE conversations ADD COLUMN pinned_at INTEGER;
ALTER TABLE conversations ADD COLUMN retention_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_conversations_user_active
ON conversations(user_id, archived_at, pinned_at, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_conversations_retention_expiry
ON conversations(archived_at)
WHERE archived_at IS NOT NULL;
`

const schemaV4 = `
CREATE TABLE IF NOT EXISTS service_settings (
	key TEXT PRIMARY KEY
		CHECK (key IN ('progressive_summary_mode')),
	value TEXT NOT NULL
		CHECK (value IN ('auto', 'off')),
	updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS service_setting_audit (
	id TEXT PRIMARY KEY,
	setting_key TEXT NOT NULL
		CHECK (setting_key IN ('progressive_summary_mode')),
	action TEXT NOT NULL
		CHECK (action IN ('update', 'recheck')),
	old_value TEXT NOT NULL
		CHECK (old_value IN ('auto', 'off')),
	new_value TEXT NOT NULL
		CHECK (new_value IN ('auto', 'off')),
	actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_service_setting_audit_created
ON service_setting_audit(created_at DESC);

INSERT INTO service_settings(key, value, updated_by, updated_at)
VALUES(
	'progressive_summary_mode',
	'auto',
	NULL,
	CAST(strftime('%s', 'now') AS INTEGER) * 1000
)
ON CONFLICT(key) DO NOTHING;

DELETE FROM provider_items WHERE item_type = 'reasoning';
`

const schemaV5 = `
CREATE TABLE IF NOT EXISTS speech_service_settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
	provider TEXT NOT NULL DEFAULT 'aliyun',
	default_voice TEXT NOT NULL DEFAULT 'longxiaochun',
	updated_by TEXT REFERENCES users(id) ON DELETE SET NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS speech_service_setting_audit (
	id TEXT PRIMARY KEY,
	old_value_json TEXT NOT NULL,
	new_value_json TEXT NOT NULL,
	actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_speech_service_setting_audit_created
ON speech_service_setting_audit(created_at DESC);

CREATE TABLE IF NOT EXISTS user_speech_preferences (
	user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	mode TEXT NOT NULL DEFAULT 'manual' CHECK (mode IN ('manual', 'auto')),
	speed REAL NOT NULL DEFAULT 1.0 CHECK (speed >= 0.5 AND speed <= 2.0),
	voice TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);

INSERT INTO speech_service_settings(
	id, enabled, provider, default_voice, updated_by, updated_at
)
VALUES(
	1,
	0,
	'aliyun',
	'longxiaochun',
	NULL,
	CAST(strftime('%s', 'now') AS INTEGER) * 1000
)
ON CONFLICT(id) DO NOTHING;
`

const schemaV6 = `
ALTER TABLE users
ADD COLUMN initial_workbench TEXT NOT NULL DEFAULT 'general'
CHECK (initial_workbench IN ('general', 'restaurant'));

ALTER TABLE users
ADD COLUMN workbench_preference TEXT
CHECK (
	workbench_preference IS NULL OR
	workbench_preference IN ('general', 'restaurant')
);

CREATE TABLE IF NOT EXISTS workbench_assignment_audit (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
	old_value TEXT NOT NULL CHECK (old_value IN ('general', 'restaurant')),
	new_value TEXT NOT NULL CHECK (new_value IN ('general', 'restaurant')),
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workbench_assignment_audit_user
ON workbench_assignment_audit(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS restaurant_profile_facts (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	field_key TEXT NOT NULL,
	value TEXT NOT NULL,
	source_message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY(user_id, field_key)
);

CREATE TABLE IF NOT EXISTS restaurant_profile_audit (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	field_key TEXT NOT NULL,
	operation TEXT NOT NULL CHECK (operation IN ('set', 'replace', 'delete')),
	old_value TEXT,
	new_value TEXT,
	source_message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_restaurant_profile_audit_user
ON restaurant_profile_audit(user_id, created_at DESC);
`
