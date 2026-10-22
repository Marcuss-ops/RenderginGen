package workerlog

// workerlog_bridge.go owns the standard-library log bridge: lines emitted by
// un-migrated call sites are classified and folded into the same structured
// handler, so a partial migration cannot split the log stream in two formats.
import (
	"bytes"
	"context"
	slog "log/slog"
	"strings"
	"sync"
)

const maxPendingLine = 1 << 16

// bridge adapts the standard library logger to slog. It splits the byte stream
// into lines (log.Printf is line-oriented, but a payload can contain newlines)
// and classifies each line's level from the marker the call sites already use
// ("WARN: …", "ERROR processor: …", "[processor WARN] …"), so the existing
// wording becomes a real level instead of being lost.
type bridge struct {
	mu     sync.Mutex
	buf    []byte
	logger *slog.Logger
}

func newBridge(logger *slog.Logger) *bridge { return &bridge{logger: logger} }

func (b *bridge) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	for {
		i := bytes.IndexByte(b.buf, '\n')
		if i < 0 {
			break
		}
		line := string(b.buf[:i])
		b.buf = b.buf[i+1:]
		b.emit(line)
	}
	if len(b.buf) >= maxPendingLine {
		b.emit(string(b.buf))
		b.buf = b.buf[:0]
	}
	return len(p), nil
}

// emit publishes one bridged line.
func (b *bridge) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	lvl, component, msg := classify(line)
	attrs := []slog.Attr{slog.String(FieldLogger, "stdlib")}
	if component != "" {
		attrs = append(attrs, slog.String(FieldComponent, component))
	}
	b.logger.LogAttrs(context.Background(), lvl, msg, attrs...)
}

// classify derives (level, component, message) from a bridged line.
//
// It recognizes exactly the markers this codebase already writes — it never
// guesses a level from the message text, because an inferred level that disagrees
// with the caller's intent is worse than a stable default:
//
//	"WARN: no GPU detected …"            → WARN,  msg "no GPU detected …"
//	"ERROR processor: workspace …"        → ERROR, component "processor"
//	"[processor WARN] policy=… "          → WARN,  component "processor"
//	"anything else"                       → INFO,  msg unchanged
func classify(line string) (slog.Level, string, string) {
	trimmed := strings.TrimSpace(line)
	upper := strings.ToUpper(trimmed)

	// Bracketed component tag, e.g. "[processor WARN] …" or "[processor] …".
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.IndexByte(trimmed, ']'); end > 1 {
			tag := trimmed[1:end]
			rest := strings.TrimSpace(trimmed[end+1:])
			fields := strings.Fields(tag)
			component := ""
			if len(fields) > 0 {
				component = strings.ToLower(fields[0])
			}
			level := slog.LevelInfo
			for _, f := range fields[1:] {
				switch strings.ToUpper(f) {
				case "WARN", "WARNING":
					level = slog.LevelWarn
				case "ERROR", "ERR":
					level = slog.LevelError
				}
			}
			if rest == "" {
				rest = trimmed
			}
			return level, component, rest
		}
	}

	for _, marker := range []struct {
		prefix string
		level  slog.Level
	}{
		{"ERROR", slog.LevelError},
		{"WARN", slog.LevelWarn},
	} {
		if !strings.HasPrefix(upper, marker.prefix) {
			continue
		}
		rest := trimmed[len(marker.prefix):]
		// Only a separator makes it a marker: "WARNINGSHOT" is not a level.
		rest = strings.TrimLeft(rest, ": ")
		if rest == "" || (!strings.HasPrefix(trimmed[len(marker.prefix):], ":") && !strings.HasPrefix(trimmed[len(marker.prefix):], " ")) {
			continue
		}
		// "ERROR processor: …" / "WARN processor: …": the word after the marker
		// names the emitting component, which is worth a field. The candidate is
		// accepted only when it looks like a package identifier AND is followed
		// by a colon — otherwise "ERROR buffer full: …" would silently drop
		// "buffer full" from the message.
		component := ""
		if i := strings.Index(rest, ":"); i > 0 && isComponentName(rest[:i]) {
			component = strings.ToLower(rest[:i])
			rest = strings.TrimLeft(rest[i:], ": ")
		}
		return marker.level, component, rest
	}
	return slog.LevelInfo, "", trimmed
}

// isComponentName reports whether s can name an emitting package: a single
// short token made of letters, digits, '_' or '-'. Anything with a space is
// prose and stays part of the message.
func isComponentName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 24 || strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// ── job scopes ───────────────────────────────────────────────────────────

// jobState is what the registry remembers about one job id: its parent (so a
// package that received only an id can still print the run it belongs to) and
// the sinks its lines are teed into.
