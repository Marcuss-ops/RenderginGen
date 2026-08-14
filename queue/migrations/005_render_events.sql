-- render_events: append-only history of what happened to a job.
--
-- render_jobs.state says "how the job is now"; render_events reconstructs
-- "what actually happened" (JOB_CREATED, JOB_CLAIMED, RENDER_STARTED, ...).
CREATE TABLE render_events (
    id         BIGSERIAL PRIMARY KEY,
    job_id     TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_id TEXT,
    worker_id  TEXT,
    event_type TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
