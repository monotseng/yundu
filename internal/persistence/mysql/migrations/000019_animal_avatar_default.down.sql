UPDATE user_accounts SET avatar_emoji='👤' WHERE avatar_emoji='🐼';
ALTER TABLE user_accounts ALTER COLUMN avatar_emoji SET DEFAULT '👤';
