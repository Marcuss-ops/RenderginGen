package processor

import (
	"os"
	"path/filepath"
)

// planDumpEnv names a directory where the worker mirrors the compiled
// plan.json it is about to hand to Chronon.
//
// A render that dies inside the daemon reports nothing but an exit status, and
// the job workspace is deleted with the run, so the one artifact that explains
// the failure — the concrete plan the renderer actually read — is gone by the
// time anyone asks. Setting CLIPRENDER_PLAN_DUMP_DIR keeps a copy per job so a
// failing effect can be replayed frame-for-frame with chronon3d_cli. Unset (the
// default) costs one env lookup and writes nothing.
const planDumpEnv = "CLIPRENDER_PLAN_DUMP_DIR"

func dumpCompiledPlan(jobID string, plan []byte) {
	dir := os.Getenv(planDumpEnv)
	if dir == "" || jobID == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, jobID+".plan.json"), plan, 0o644)
}
