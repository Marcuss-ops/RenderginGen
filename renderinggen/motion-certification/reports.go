// Package motioncert exposes the checked-in motion certification reports as a
// machine-readable status source for the runtime animation catalog.
//
// The JSON files under latest/ are dated snapshots written by the overlay
// certification suite that runs behind the `certification` build tag. This
// package does not run that suite and certifies nothing by itself: it parses the
// recorded outcome so the compiled catalog can state, per motion, whether a
// recorded run passed, failed, or never ran.
//
// The result is a SNAPSHOT. The catalog publishes the snapshot date and the
// contributing reports next to the statuses, and a consumer must not present a
// motion no report names as certified.
package motioncert

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// reportFS holds the certification reports themselves. The directory is the
// artifact's own, so the embed path cannot silently point at a stale copy.
//
//go:embed latest/*.json
var reportFS embed.FS

// Published status values. They are the closed vocabulary of the catalog: a
// report's raw status is normalised to passed or failed, and a motion no report
// names is unverified.
const (
	StatusPassed     = "passed"
	StatusFailed     = "failed"
	StatusUnverified = "unverified"
)

// Report describes one parsed certification report.
type Report struct {
	Name        string `json:"report"`
	Scope       string `json:"scope"`
	Backend     string `json:"backend"`
	GeneratedAt string `json:"generated_at_utc"`
	Motions     int    `json:"motions"`
}

// MotionStatus is the newest recorded outcome of one motion.
type MotionStatus struct {
	MotionID    string
	Status      string
	Report      string
	GeneratedAt string
	Error       string
}

// Snapshot is the parsed status source plus the reports it came from.
type Snapshot struct {
	Reports    []Report
	Statuses   map[string]MotionStatus
	SnapshotAt string
}

type wireReport struct {
	GeneratedAtUTC  string       `json:"generated_at_utc"`
	Backend         string       `json:"backend"`
	HardwareEncoder string       `json:"hardware_encoder"`
	Motions         []wireMotion `json:"motions"`
}

type wireMotion struct {
	MotionID string `json:"motion_id"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}

// Load parses every embedded report. A motion named by more than one report
// takes its status from the newest report, so a later run refreshes an earlier
// result instead of both being published; a motion no report names is left for
// the caller to publish as unverified.
func Load() (Snapshot, error) {
	entries, err := reportFS.ReadDir("latest")
	if err != nil {
		return Snapshot{}, fmt.Errorf("motioncert: read embedded reports: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return Snapshot{}, fmt.Errorf("motioncert: no certification report embedded under latest/")
	}

	type parsed struct {
		report Report
		when   time.Time
		result []wireMotion
	}
	reports := make([]parsed, 0, len(names))
	for _, name := range names {
		raw, err := reportFS.ReadFile(path.Join("latest", name))
		if err != nil {
			return Snapshot{}, fmt.Errorf("motioncert: read %s: %w", name, err)
		}
		var decoded wireReport
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return Snapshot{}, fmt.Errorf("motioncert: decode %s: %w", name, err)
		}
		when, err := time.Parse(time.RFC3339, decoded.GeneratedAtUTC)
		if err != nil {
			return Snapshot{}, fmt.Errorf("motioncert: %s has an unparseable generated_at_utc %q: %w", name, decoded.GeneratedAtUTC, err)
		}
		reports = append(reports, parsed{
			report: Report{
				Name:        name,
				Scope:       strings.TrimSuffix(name, "-runtime-report.json"),
				Backend:     strings.TrimSpace(decoded.Backend + " " + decoded.HardwareEncoder),
				GeneratedAt: when.UTC().Format(time.RFC3339),
				Motions:     len(decoded.Motions),
			},
			when:   when,
			result: decoded.Motions,
		})
	}
	// Newest last, so replaying the slice leaves the newest outcome in place.
	// The name sort above keeps equal timestamps deterministic.
	sort.SliceStable(reports, func(i, j int) bool { return reports[i].when.Before(reports[j].when) })

	snapshot := Snapshot{Statuses: make(map[string]MotionStatus)}
	for _, report := range reports {
		snapshot.Reports = append(snapshot.Reports, report.report)
		for _, motion := range report.result {
			if motion.MotionID == "" {
				continue
			}
			snapshot.Statuses[motion.MotionID] = MotionStatus{
				MotionID:    motion.MotionID,
				Status:      publishedStatus(motion.Status),
				Report:      report.report.Name,
				GeneratedAt: report.report.GeneratedAt,
				Error:       motion.Error,
			}
		}
	}
	snapshot.SnapshotAt = reports[len(reports)-1].report.GeneratedAt
	return snapshot, nil
}

// Status returns the recorded outcome of one motion, or an unverified entry when
// no report names it. Unverified is a result, not a missing value: the caller
// publishes it so "never recorded" cannot be read as "certified".
func (s Snapshot) Status(id string) MotionStatus {
	if status, ok := s.Statuses[id]; ok {
		return status
	}
	return MotionStatus{MotionID: id, Status: StatusUnverified}
}

// Counts tallies the published status of the given motion IDs. A motion the
// reports never named counts as unverified.
func (s Snapshot) Counts(ids []string) (passed, failed, unverified int) {
	for _, id := range ids {
		switch s.Status(id).Status {
		case StatusPassed:
			passed++
		case StatusFailed:
			failed++
		default:
			unverified++
		}
	}
	return passed, failed, unverified
}

// publishedStatus normalises a recorded status to the closed catalog
// vocabulary. Anything that is not a pass is a failure: a partially understood
// status must never be presented as certified.
func publishedStatus(recorded string) string {
	if strings.EqualFold(strings.TrimSpace(recorded), "passed") {
		return StatusPassed
	}
	return StatusFailed
}
