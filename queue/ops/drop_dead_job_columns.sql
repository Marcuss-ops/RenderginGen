-- Operator-run cleanup: drop the dead render_jobs columns and their index.
--
-- WHY THIS IS NOT A MIGRATION
-- ---------------------------
-- queue/internal/migrate applies every file in queue/migrations at service
-- startup, so a DROP placed there would execute automatically on the next
-- `queued` deploy — an irreversible schema change triggered by a restart that
-- nobody reviewed as a schema change. This file lives in ops/ precisely so a
-- human runs it deliberately, after a backup, against a known database.
--
-- WHAT IS DEAD (evidence, not assumption)
-- ---------------------------------------
--   render_jobs.workflow_id     migrations/001_render_jobs.sql:8
--   render_jobs.source_job_id   migrations/001_render_jobs.sql:9
--   idx on render_jobs(workflow_id)  migrations/007_indexes.sql:13
--
--   * No Go source in the queue module reads or writes either column: the
--     INSERT in postgres/repository.go lists its columns explicitly and never
--     names them, no SELECT projects them, and no filter references them.
--   * migrations/023_drop_retry_wait_state.sql already documents them as
--     "dead columns kept only for backward compatibility with external
--     producers — new code must not use them".
--   * Consequence of leaving them: both columns stay NULL on every row while
--     the index is maintained on EVERY insert and every state transition
--     (claim, renew, complete, fail, requeue) of the queue's hottest table.
--
-- ORDER OF OPERATIONS
-- -------------------
-- 1. Verify nothing external depends on them. The columns exist for external
--    producers: check the consumers you actually have before dropping.
-- 2. Take a backup (or at minimum confirm a recent one).
-- 3. Run each statement separately, outside a transaction, and watch for locks.
--
-- VERIFICATION BEFORE RUNNING (expect 0 and no other output)
--   SELECT count(*) FROM render_jobs WHERE workflow_id IS NOT NULL;
--   SELECT count(*) FROM render_jobs WHERE source_job_id IS NOT NULL;
-- A non-zero count means the columns DO carry data: stop and find the writer
-- before dropping anything.

-- 1. The index on the dead column. DROP INDEX CONCURRENTLY cannot run inside a
--    transaction block, which is why this file is not a migration.
DROP INDEX CONCURRENTLY IF EXISTS render_jobs_workflow_id_idx;

-- 2. The columns themselves. DROP COLUMN takes an ACCESS EXCLUSIVE lock for the
--    duration of the operation; on a large table prefer to schedule it during
--    a quiet window. IF EXISTS keeps the file re-runnable.
ALTER TABLE render_jobs DROP COLUMN IF EXISTS workflow_id;
ALTER TABLE render_jobs DROP COLUMN IF EXISTS source_job_id;

-- AFTER RUNNING
--   * migrations/001 and 007 keep creating/serving these objects for a fresh
--     database, so a NEW deployment still needs the history rewritten or a
--     follow-up migration that drops them for new installs. Prefer the latter:
--     it is idempotent with this file (both use IF EXISTS).
--   * Update the comment in migrations/023_drop_retry_wait_state.sql, which
--     currently states the two columns are kept.
