ALTER TABLE user_accounts ADD COLUMN avatar_emoji VARCHAR(16) NOT NULL DEFAULT '👤' AFTER display_name;
