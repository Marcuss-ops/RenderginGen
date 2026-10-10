package config

import "testing"

// TestSocketPathsSelectTheMultiDaemonShape: the list is real configuration, it
// resolves to the render topology, and the unused single-socket default is not
// staged beside it (an operator reading the effective config would otherwise
// see a daemon the worker never dials).
func TestSocketPathsSelectTheMultiDaemonShape(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig+`chronon:
  mode: ipc
  socket_paths:
    - /run/chronon/a.sock
    - /run/chronon/b.sock
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := cfg.Chronon.RenderSockets()
	if len(got) != 2 || got[0] != "/run/chronon/a.sock" {
		t.Fatalf("RenderSockets = %v, want the two declared sockets in order", got)
	}
	if cfg.Chronon.SocketPath != "" {
		t.Errorf("a worker with socket_paths must not also carry the default single socket, got %q", cfg.Chronon.SocketPath)
	}
}

// TestSocketPathsRejectWhatIsNotADaemon: each socket is one daemon, so a blank
// or repeated entry would spread lanes over something that is not a daemon —
// silently halving throughput. It must fail at load instead.
func TestSocketPathsRejectWhatIsNotADaemon(t *testing.T) {
	cases := map[string]string{
		"empty entry": minimalConfig + "chronon:\n  mode: ipc\n  socket_paths:\n    - \"\"\n",
		"repeated":    minimalConfig + "chronon:\n  mode: ipc\n  socket_paths:\n    - /run/a.sock\n    - /run/a.sock\n",
	}
	for name, doc := range cases {
		if _, err := Load(writeConfig(t, doc)); err == nil {
			t.Errorf("%s: a doubtful socket list must fail at load, not at the first render", name)
		}
	}
}

// TestSingleSocketPathIsUnchanged keeps the historical one-daemon shape
// working: no list, one default socket, one lane pool.
func TestSingleSocketPathIsUnchanged(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig+"chronon:\n  mode: ipc\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Chronon.SocketPaths) != 0 {
		t.Fatalf("socket_paths must stay empty when only socket_path is used, got %v", cfg.Chronon.SocketPaths)
	}
	if got := cfg.Chronon.RenderSockets(); len(got) != 1 || got[0] == "" {
		t.Fatalf("the single-daemon shape must resolve to one socket, got %v", got)
	}
}
