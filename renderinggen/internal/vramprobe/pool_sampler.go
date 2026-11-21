package vramprobe

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// pool_sampler.go owns the phase sampler: the goroutine that samples VRAM on
// its own clock, the phase switch the run drives, and the sample file it
// writes. It never decides when a phase starts — pool.go does.

// poolSampler samples nvidia-smi on a ticker and tags every observation with the
// phase that was active when it was taken.
//
// The phase is carried by an atomic string rather than by restarting the
// sampler per phase, so the transition itself cannot drop or duplicate samples
// around the interesting moment (a pool starting, a render beginning).
type poolSampler struct {
	interval time.Duration
	ctx      context.Context

	phaseMu sync.Mutex
	phase   poolPhase

	samplesMu sync.Mutex
	samples   []poolSample

	started time.Time
	stopCh  chan struct{}
	doneCh  chan struct{}
	once    sync.Once

	counter atomic.Int64
}

func newPoolSampler(ctx context.Context, interval time.Duration) *poolSampler {
	sampler := &poolSampler{
		interval: interval,
		ctx:      ctx,
		phase:    phaseIdle,
		started:  time.Now(),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go sampler.loop()
	return sampler
}

func (s *poolSampler) loop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			device, utilization, err := deviceSample(s.ctx)
			if err != nil {
				continue
			}
			entry := poolSample{
				phase:       s.currentPhase(),
				at:          time.Since(s.started),
				device:      device,
				daemon:      daemonMiB(s.ctx),
				utilization: utilization,
			}
			s.samplesMu.Lock()
			s.samples = append(s.samples, entry)
			s.samplesMu.Unlock()
			s.counter.Add(1)
		}
	}
}

func (s *poolSampler) setPhase(phase poolPhase) {
	s.phaseMu.Lock()
	s.phase = phase
	s.phaseMu.Unlock()
}

func (s *poolSampler) currentPhase() poolPhase {
	s.phaseMu.Lock()
	defer s.phaseMu.Unlock()
	return s.phase
}

// snapshot takes one immediate out-of-band observation (the idle baseline), so
// the baseline does not wait for a ticker edge.
func (s *poolSampler) snapshot() poolSample {
	device, utilization, err := deviceSample(s.ctx)
	entry := poolSample{phase: s.currentPhase(), at: time.Since(s.started), device: device, utilization: utilization}
	if err == nil {
		entry.daemon = daemonMiB(s.ctx)
	}
	s.samplesMu.Lock()
	s.samples = append(s.samples, entry)
	s.samplesMu.Unlock()
	s.counter.Add(1)
	return entry
}

func (s *poolSampler) stop() {
	s.once.Do(func() {
		close(s.stopCh)
		<-s.doneCh
	})
}

func (s *poolSampler) count() int {
	return int(s.counter.Load())
}

func (s *poolSampler) all() []poolSample {
	s.samplesMu.Lock()
	defer s.samplesMu.Unlock()
	out := make([]poolSample, len(s.samples))
	copy(out, s.samples)
	return out
}

// phaseFacts reduces the raw series to the per-phase numbers the report carries.
// The DELTA is always computed against the phase's own first sample: a device
// that is shared with other tenants drifts, so a global baseline would attribute
// someone else's growth to this harness.
func (s *poolSampler) phaseFacts() map[string]PoolPhaseFacts {
	facts := map[string]PoolPhaseFacts{}
	for _, entry := range s.all() {
		key := string(entry.phase)
		current, ok := facts[key]
		if !ok {
			current = PoolPhaseFacts{
				IdleDeviceMiB: entry.device,
				IdleDaemonMiB: entry.daemon,
			}
		}
		current.Samples++
		if entry.device > current.PeakDeviceMiB {
			current.PeakDeviceMiB = entry.device
		}
		if entry.daemon > current.PeakDaemonMiB {
			current.PeakDaemonMiB = entry.daemon
		}
		if entry.utilization > current.PeakUtilization {
			current.PeakUtilization = entry.utilization
		}
		current.DeviceDeltaMiB = current.PeakDeviceMiB - current.IdleDeviceMiB
		current.DaemonDeltaMiB = current.PeakDaemonMiB - current.IdleDaemonMiB
		facts[key] = current
	}
	return facts
}

// writeSamples writes the raw series, so a reader can re-derive any statistic
// instead of trusting the summary.
func (s *poolSampler) writeSamples(path string) error {
	var builder strings.Builder
	builder.WriteString("# phase elapsed_s device_used_mib chronon3d_cli_used_mib gpu_util_pct\n")
	for _, entry := range s.all() {
		fmt.Fprintf(&builder, "%s %.2f %d %d %d\n", entry.phase, entry.at.Seconds(), entry.device, entry.daemon, entry.utilization)
	}
	return writeFile(path, []byte(builder.String()))
}
