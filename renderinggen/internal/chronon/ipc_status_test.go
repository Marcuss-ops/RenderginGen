package chronon

import (
	"context"
	"strings"
	"testing"
)

// The IPC client classifies every non-ok reply status through ipcReplyError, so
// a daemon refusal that the client's mirror knows is reported by NAME, and a
// code the mirror does NOT know is reported as protocol drift rather than as an
// ordinary failure.
//
// The distinction matters because the two cases need opposite responses: a known
// Error/NotFound is the daemon answering the request, while an unrecognized code
// means the daemon is speaking a revision this client was not built against —
// retrying or interpreting it as "just an error" would hide that.

// TestIPCClientUnknownStatusIsReportedAsDrift drives a real request through the
// fake daemon and asserts the client surfaces the unknown code as drift, with
// the code, the protocol revision and the daemon's own message all present.
func TestIPCClientUnknownStatusIsReportedAsDrift(t *testing.T) {
	// 7 is not a status in the protocol v1 mirror (Ok/Error/NotFound/BadRequest/
	// Shutdown are 0..4); it stands in for a status a newer daemon might add.
	const unknownStatus uint32 = 7
	socketPath, _, _ := startFakeDaemon(t, unknownStatus, "rendered 12 frames as a new status")

	err := NewIPCClient(socketPath).Shutdown(context.Background())
	if err == nil {
		t.Fatal("an unrecognized reply status must be an error")
	}
	for _, want := range []string{"7", "protocol v1", "rendered 12 frames as a new status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	for _, banned := range []string{"Ok", "NotFound", "BadRequest"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("error = %q, want it not to claim the unknown status is %q", err, banned)
		}
	}
}

// TestIPCClientKnownStatusIsReportedByName pins the other half: a status the
// mirror knows is named, so an operator reading the log does not have to look up
// the number.
func TestIPCClientKnownStatusIsReportedByName(t *testing.T) {
	cases := []struct {
		status uint32
		name   string
	}{
		{ipcStatusError, "Error"},
		{ipcStatusNotFound, "NotFound"},
		{ipcStatusBadRequest, "BadRequest"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			socketPath, _, _ := startFakeDaemon(t, tc.status, "detail")
			_, err := NewIPCClient(socketPath).Status(context.Background())
			if err == nil {
				t.Fatalf("status %d must be an error", tc.status)
			}
			for _, want := range []string{tc.name, "detail"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "protocol v") {
				t.Errorf("error = %q, want the KNOWN status %s reported by name, not as drift", err, tc.name)
			}
		})
	}
}

// TestIPCEveryCallSiteUsesTheSharedStatusFormatter pins that no call site
// regressed to a formatted number: the four commands that can receive a non-ok
// reply must all go through ipcReplyError, so a new status cannot be described
// differently depending on which verb it interrupted.
func TestIPCEveryCallSiteUsesTheSharedStatusFormatter(t *testing.T) {
	cases := []struct {
		name string
		call func(*IPCClient) error
	}{
		{"status", func(c *IPCClient) error { _, err := c.Status(context.Background()); return err }},
		{"prefetch", func(c *IPCClient) error { return c.PrefetchAsset(context.Background(), "/assets/a.png") }},
		{"shutdown", func(c *IPCClient) error { return c.Shutdown(context.Background()) }},
		{"assemble", func(c *IPCClient) error {
			return c.Assemble(context.Background(), AssembleRequest{Inputs: []string{"a.mp4"}, Output: "o.mp4"})
		}},
	}
	const driftStatus uint32 = 42
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			socketPath, _, _ := startFakeDaemon(t, driftStatus, "unrecognized")
			err := tc.call(NewIPCClient(socketPath))
			if err == nil {
				t.Fatal("a non-ok reply must be an error")
			}
			if !strings.Contains(err.Error(), "protocol v1") {
				t.Errorf("%s: error = %q, want the shared unknown-status wording", tc.name, err)
			}
			if !strings.Contains(err.Error(), "ipc "+tc.name) {
				t.Errorf("%s: error = %q, want the op prefix %q", tc.name, err, "ipc "+tc.name)
			}
		})
	}
}
