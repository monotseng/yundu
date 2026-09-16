DELETE FROM schema_migrations WHERE version=15;
DROP TABLE IF EXISTS ai_concurrency_slots;
DROP TABLE IF EXISTS ai_monthly_usage;
DROP TABLE IF EXISTS ai_daily_usage;
DROP TABLE IF EXISTS ai_invocations;
