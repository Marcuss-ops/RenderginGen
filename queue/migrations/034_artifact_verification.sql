-- render_artifacts.verification: what the worker proved about the artifact.
--
-- The verification policy (fast / sample / normal / certify / strict) was only
-- visible in the worker log and in the receipt. A consumer could see an
-- artifact and could not tell whether its composition had been verified,
-- recorded as unverifiable, or was not required at all. The block is nullable
-- so artifacts written before this column existed stay valid and mean
-- "not recorded".
ALTER TABLE render_artifacts
    ADD COLUMN IF NOT EXISTS verification JSONB;
