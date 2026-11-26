-- Every render job declares one execution policy. Before this column the
-- producer's require_gpu demand was decoded by the client and dropped here, so
-- the worker could only guess from its own configuration: a job that demanded
-- the GPU lane could be served by the software lane without anyone noticing.
--
-- The default is the historical behaviour (GPU preferred, software fallback
-- when the worker allows it), so rows submitted by any producer that predates
-- the field keep exactly the semantics they had.
ALTER TABLE render_jobs
    ADD COLUMN execution_policy TEXT NOT NULL DEFAULT 'gpu_preferred';

-- The vocabulary is pinned to client.AllExecutionPolicies() by
-- queue/internal/model/execution_policy_schema_test.go: a value the Go side
-- accepts but SQL rejects (or the reverse) is a silent routing failure.
ALTER TABLE render_jobs
    ADD CONSTRAINT render_jobs_execution_policy_check
    CHECK (execution_policy IN ('gpu_only', 'gpu_preferred', 'cpu_only'));
