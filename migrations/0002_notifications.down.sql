DROP INDEX IF EXISTS users_notify_idx;
ALTER TABLE users DROP COLUMN IF EXISTS notify_enabled;
