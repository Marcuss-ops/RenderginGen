package chronon

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The IPC client mirrors Chronon3d's chronon_ipc.hpp BY HAND: a magic number, a
// protocol version, a header size, a payload cap and two enums. Nothing on the
// wire carries the version (the 12-byte header is magic | command-or-status |
// payload-len), so a header this client was not built against can only be
// detected by comparing the mirror against the header itself.
//
// These tests do exactly that: when the sibling Chronon3d checkout is present
// they parse the real header and hold every constant to it. When it is absent
// they SKIP — never fail — matching the cross-repo convention already used by
// internal/overlay/contract_boundary_test.go, so a standalone RenderingGen
// checkout (including this repository's own CI) reports an honest skip.
//
// The half that needs no checkout is TestIPCWireContractValuesArePinnedToV1:
// the protocol revision this client speaks is written down as literals, so an
// accidental edit to a constant fails even where the header cannot be read.

// ipcHeaderPathEnv points at a chronon_ipc.hpp when the sibling checkout is not
// where discovery looks (a packed CI cache, a read-only engine image).
const ipcHeaderPathEnv = "CHRONON_IPC_HEADER"

// ipcHeaderRelativePath is where the header lives inside a Chronon3d checkout.
const ipcHeaderRelativePath = "Chronon3d/apps/chronon3d_cli/daemon/chronon_ipc.hpp"

// ipcHeaderSource returns the text of chronon_ipc.hpp, or skips the test when
// the header cannot be reached from this checkout (see chronon_source_test.go).
func ipcHeaderSource(t *testing.T) string {
	t.Helper()
	return chrononSourceFile(t, ipcHeaderRelativePath, ipcHeaderPathEnv)
}

var (
	cppBlockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cppLineCommentRE  = regexp.MustCompile(`//[^\n]*`)
)

// stripCppComments removes line and block comments from a header.
//
// This is required before anything structural is read: the header's enum
// documentation contains braces (`///< payload: JSON {input_paths,
// output_path}`), so a brace-delimited scan over the raw text would end the
// enum early and then read unrelated declarations as enumerators.
func stripCppComments(src string) string {
	return cppLineCommentRE.ReplaceAllString(cppBlockCommentRE.ReplaceAllString(src, ""), "")
}

// cppConstantRE matches one `inline constexpr <type> <name> = <expr>;` line.
func cppConstantRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*inline\s+constexpr\s+[^;=]+?\b` + regexp.QuoteMeta(name) + `\s*=\s*([^;]+);`)
}

// parseCppUintConstant reads a constexpr numeric constant. The value may be a
// product of literals (the header writes the payload cap as
// `64u * 1024u * 1024u`), and comments are ignored. It returns an error rather
// than failing the test directly so its rejection paths are themselves
// testable.
func parseCppUintConstant(src, name string) (uint32, error) {
	match := cppConstantRE(name).FindStringSubmatch(stripCppComments(src))
	if match == nil {
		return 0, fmt.Errorf("no constant %q declared", name)
	}
	expr := match[1]
	product := uint64(1)
	for _, term := range strings.Split(expr, "*") {
		token := strings.TrimSpace(term)
		token = strings.TrimRight(token, "uUlL")
		token = strings.TrimSpace(token)
		if token == "" {
			return 0, fmt.Errorf("constant %s: empty term in %q", name, match[1])
		}
		base := 10
		digits := token
		if strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0X") {
			base = 16
			digits = token[2:]
		}
		value, err := strconv.ParseUint(digits, base, 64)
		if err != nil {
			return 0, fmt.Errorf("constant %s: %q is not a numeric literal", name, token)
		}
		product *= value
	}
	return uint32(product), nil
}

// mustCppUintConstant is the failing wrapper the conformance tests use.
func mustCppUintConstant(t *testing.T, src, name string) uint32 {
	t.Helper()
	value, err := parseCppUintConstant(src, name)
	if err != nil {
		t.Fatalf("chronon_ipc.hpp: %v", err)
	}
	return value
}

// cppEnumBodyRE captures the body of `enum class <name> : std::uint32_t { ... }`.
func cppEnumBodyRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)enum\s+class\s+` + regexp.QuoteMeta(name) + `\s*:\s*std::uint32_t\s*\{(.*?)\}`)
}

// parseCppEnum reads `enum class <name> : std::uint32_t` into a name→value map.
// Trailing `///<` documentation and `//` comments are stripped per entry, and
// every entry must carry an explicit value: an implicit successor would make
// the enum's numbering depend on declaration order, which the client cannot
// verify.
func parseCppEnum(src, name string) (map[string]uint32, error) {
	match := cppEnumBodyRE(name).FindStringSubmatch(stripCppComments(src))
	if match == nil {
		return nil, fmt.Errorf("no enum class %s : std::uint32_t declared", name)
	}
	values := make(map[string]uint32)
	for _, entry := range strings.Split(match[1], ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("enum %s: entry %q has no explicit value", name, entry)
		}
		key := strings.TrimSpace(parts[0])
		raw := strings.TrimRight(strings.TrimSpace(parts[1]), "uUlL")
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("enum %s: entry %q value %q is not a decimal literal", name, key, raw)
		}
		if _, dup := values[key]; dup {
			return nil, fmt.Errorf("enum %s: entry %q is declared twice", name, key)
		}
		values[key] = uint32(value)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("enum %s: parsed no entries", name)
	}
	return values, nil
}

// mustCppEnum is the failing wrapper the conformance tests use.
func mustCppEnum(t *testing.T, src, name string) map[string]uint32 {
	t.Helper()
	values, err := parseCppEnum(src, name)
	if err != nil {
		t.Fatalf("chronon_ipc.hpp: %v", err)
	}
	return values
}

// TestStripCppComments pins the comment stripper the structural scans depend
// on, including the brace-in-a-comment case that would otherwise end an enum
// early.
func TestStripCppComments(t *testing.T) {
	src := `// leading
enum class A : std::uint32_t { /* inline */ Ok = 0, };
///< payload: JSON {input_paths, output_path}
const int x = 1; // trailing`
	got := stripCppComments(src)
	for _, banned := range []string{"leading", "inline", "input_paths", "trailing"} {
		if strings.Contains(got, banned) {
			t.Errorf("stripCppComments left %q in %q", banned, got)
		}
	}
	if !strings.Contains(got, "Ok = 0") {
		t.Errorf("stripCppComments removed code: %q", got)
	}
}

// TestParseCppEnumStripsComments pins the parser against the header's own
// formatting, so a parse failure is never mistaken for a contract violation.
func TestParseCppEnumStripsComments(t *testing.T) {
	src := `
enum class Command : std::uint32_t {
    PrefetchAsset = 1,   ///< payload: asset path → warm asset cache
    PreparePlan   = 2,   ///< payload: composition id → compile + plan once
    Status        = 4,   ///< no payload → engine statistics
};`
	got, err := parseCppEnum(src, "Command")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint32{"PrefetchAsset": 1, "PreparePlan": 2, "Status": 4}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("parsed %v, want %v", got, want)
		}
	}
}

// TestParseCppEnumRejectsImplicitValue fails closed on an entry without an
// explicit value: the client cannot verify numbering it did not read.
func TestParseCppEnumRejectsImplicitValue(t *testing.T) {
	for _, body := range []string{"Ok = 0,\n    Error,\n", "Ok\n"} {
		src := "enum class Status : std::uint32_t {\n    " + body + "};"
		if _, err := parseCppEnum(src, "Status"); err == nil {
			t.Errorf("body %q: an entry without an explicit value must fail the parse", body)
		}
	}
}

// TestParseCppEnumRejectsDuplicates guards the map the comparison is built on.
func TestParseCppEnumRejectsDuplicates(t *testing.T) {
	src := "enum class Status : std::uint32_t {\n    Ok = 0,\n    Ok = 1,\n};"
	if _, err := parseCppEnum(src, "Status"); err == nil {
		t.Error("a duplicated enumerator must fail the parse")
	}
}

// TestParseCppEnumRejectsMissingEnum keeps an enum rename visible as an error.
func TestParseCppEnumRejectsMissingEnum(t *testing.T) {
	if _, err := parseCppEnum("enum class Other : std::uint32_t { Ok = 0 };", "Status"); err == nil {
		t.Error("a missing enum must fail the parse")
	}
}

// TestParseCppUintConstantEvaluatesProduct pins the product evaluation the
// payload cap relies on (`64u * 1024u * 1024u`) and its rejection of anything
// that is not a numeric literal.
func TestParseCppUintConstantEvaluatesProduct(t *testing.T) {
	cases := []struct {
		src  string
		want uint32
	}{
		{"inline constexpr std::uint32_t kMagic = 0x43484e33u;  // \"CHN3\"", 0x43484e33},
		{"inline constexpr std::uint32_t kVersion = 1u;", 1},
		{"inline constexpr std::size_t kHeaderBytes = 12u;  // 3 * u32", 12},
		{"inline constexpr std::size_t kMaxPayloadBytes = 64u * 1024u * 1024u;  // 64 MiB", 64 * 1024 * 1024},
	}
	for _, tc := range cases {
		name := "kMaxPayloadBytes"
		if idx := strings.Index(tc.src, " k"); idx >= 0 {
			rest := tc.src[idx+1:]
			name = rest[:strings.IndexAny(rest, " =\t")]
		}
		got, err := parseCppUintConstant(tc.src, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %d, want %d", name, got, tc.want)
		}
	}
	if _, err := parseCppUintConstant("inline constexpr int kX = kNope;", "kX"); err == nil {
		t.Error("a non-numeric constant must fail the parse")
	}
	if _, err := parseCppUintConstant("inline constexpr int kOther = 1;", "kX"); err == nil {
		t.Error("a missing constant must fail the parse")
	}
}

// TestIPCWireContractValuesArePinnedToV1 is the checkout-independent half: the
// protocol revision this client speaks, written down once. It protects a
// standalone RenderingGen checkout, where the header cannot be read, from an
// accidental edit that would renumber the wire under a running daemon.
func TestIPCWireContractValuesArePinnedToV1(t *testing.T) {
	if ipcMagic != 0x43484e33 {
		t.Errorf("ipcMagic = %#x, want 0x43484e33 (\"CHN3\")", ipcMagic)
	}
	if ipcProtocolVersion != 1 {
		t.Errorf("ipcProtocolVersion = %d, want 1", ipcProtocolVersion)
	}
	if ipcHeaderBytes != 12 {
		t.Errorf("ipcHeaderBytes = %d, want 12", ipcHeaderBytes)
	}
	if ipcMaxPayload != 64*1024*1024 {
		t.Errorf("ipcMaxPayload = %d, want %d", ipcMaxPayload, 64*1024*1024)
	}
	wantCommands := map[uint32]string{
		1: "PrefetchAsset",
		2: "PreparePlan",
		3: "RenderOverlay",
		4: "Status",
		5: "Shutdown",
		6: "RenderJob",
		7: "AssembleSegments",
	}
	assertStatusTable(t, "command", wantCommands, ipcCommandNames)
	wantStatuses := map[uint32]string{
		0: "Ok",
		1: "Error",
		2: "NotFound",
		3: "BadRequest",
		4: "Shutdown",
	}
	assertStatusTable(t, "reply status", wantStatuses, ipcStatusNames)
}

// assertStatusTable compares a value→name mirror against its expected contents
// in both directions, so neither a changed value nor a renamed entry hides.
func assertStatusTable(t *testing.T, label string, want, got map[uint32]string) {
	t.Helper()
	values := make([]int, 0, len(want))
	for value := range want {
		values = append(values, int(value))
	}
	sort.Ints(values)
	for _, value := range values {
		code := uint32(value)
		if got[code] != want[code] {
			t.Errorf("%s %d = %q, want %q", label, code, got[code], want[code])
		}
	}
	for code := range got {
		if _, ok := want[code]; !ok {
			t.Errorf("%s %d = %q is not part of protocol v1", label, code, got[code])
		}
	}
}

// TestIPCWireConstantsMatchChrononHeader holds magic, version, header size and
// payload cap to the engine's header.
func TestIPCWireConstantsMatchChrononHeader(t *testing.T) {
	src := ipcHeaderSource(t)

	if got, want := ipcMagic, mustCppUintConstant(t, src, "kProtocolMagic"); got != want {
		t.Errorf("ipcMagic = %#x, header kProtocolMagic = %#x", got, want)
	}
	if got, want := ipcProtocolVersion, mustCppUintConstant(t, src, "kProtocolVersion"); got != want {
		t.Errorf("ipcProtocolVersion = %d, header kProtocolVersion = %d: the mirror is a revision behind the engine", got, want)
	}
	if got, want := uint32(ipcHeaderBytes), mustCppUintConstant(t, src, "kHeaderBytes"); got != want {
		t.Errorf("ipcHeaderBytes = %d, header kHeaderBytes = %d", got, want)
	}
	if got, want := uint32(ipcMaxPayload), mustCppUintConstant(t, src, "kMaxPayloadBytes"); got != want {
		t.Errorf("ipcMaxPayload = %d, header kMaxPayloadBytes = %d", got, want)
	}
}

// TestIPCCommandEnumMatchesChrononHeader holds the command mirror to the
// header's Command enum.
//
// The client only ever SENDS commands it knows, so a command added by the
// engine is not automatically a client bug — but it is a contract change the
// client must acknowledge rather than discover at the socket. The comparison is
// therefore two-way on purpose: a value that moved is a hard failure (the
// client would send one command and the daemon would perform another), and a
// new engine command is a failure that says to mirror it and bump
// ipcProtocolVersion.
func TestIPCCommandEnumMatchesChrononHeader(t *testing.T) {
	src := ipcHeaderSource(t)
	header := mustCppEnum(t, src, "Command")

	if len(header) != len(ipcCommandNames) {
		t.Fatalf("header Command has %d entries, this client mirrors %d: mirror the new command(s) and bump ipcProtocolVersion\nheader: %v\nclient: %v",
			len(header), len(ipcCommandNames), header, ipcCommandNames)
	}
	for name, value := range header {
		mirrored, ok := ipcCommandNames[value]
		if !ok {
			t.Errorf("header Command %s = %d is not mirrored in ipcCommandNames", name, value)
			continue
		}
		if mirrored != name {
			t.Errorf("command value %d is %q in the header but %q in ipcCommandNames: the mirror misnumbers the wire", value, name, mirrored)
		}
	}
	for value, name := range ipcCommandNames {
		if _, ok := header[name]; !ok {
			t.Errorf("ipcCommandNames %s = %d has no counterpart in the header Command enum", name, value)
		}
	}
}

// TestIPCStatusEnumMatchesChrononHeader holds the status mirror to the header's
// Status enum, in BOTH directions.
//
// Unlike the command enum, this one must be complete: every reply carries a
// status, so a value missing from ipcStatusNames is a reply this client cannot
// interpret. It is also the table ipcReplyError uses to tell a daemon refusal
// from protocol drift, which is why an entry removed from the header must fail
// here rather than quietly becoming an "unknown" code.
func TestIPCStatusEnumMatchesChrononHeader(t *testing.T) {
	src := ipcHeaderSource(t)
	header := mustCppEnum(t, src, "Status")

	if len(header) != len(ipcStatusNames) {
		t.Fatalf("header Status has %d entries, this client mirrors %d: every reply status must be interpretable\nheader: %v\nclient: %v",
			len(header), len(ipcStatusNames), header, ipcStatusNames)
	}
	for name, value := range header {
		mirrored, ok := ipcStatusNames[value]
		if !ok {
			t.Errorf("header Status %s = %d is not mirrored in ipcStatusNames", name, value)
			continue
		}
		if mirrored != name {
			t.Errorf("status value %d is %q in the header but %q in ipcStatusNames", value, name, mirrored)
		}
	}
	for value, name := range ipcStatusNames {
		if _, ok := header[name]; !ok {
			t.Errorf("ipcStatusNames %s = %d has no counterpart in the header Status enum", name, value)
		}
	}
}

// TestIPCStatusMirrorIsExhaustiveForErrorReporting pins the invariant
// ipcReplyError depends on: for every status the mirror knows, the error names
// it, and for any other value the error reports protocol drift instead of
// inventing a meaning.
func TestIPCStatusMirrorIsExhaustiveForErrorReporting(t *testing.T) {
	for code, name := range ipcStatusNames {
		got, known := describeIPCStatus(code)
		if !known || got != name {
			t.Errorf("describeIPCStatus(%d) = (%q, %v), want (%q, true)", code, got, known, name)
		}
		err := ipcReplyError("render", code, "boom")
		if err == nil {
			t.Fatalf("ipcReplyError(%d) returned nil", code)
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), fmt.Sprintf("(%d)", code)) {
			t.Errorf("ipcReplyError(%d, ...) = %q, want it to name %q and the numeric code", code, err, name)
		}
	}

	const unknown = uint32(99)
	if name, known := describeIPCStatus(unknown); known || name != "" {
		t.Fatalf("describeIPCStatus(%d) = (%q, %v), want (\"\", false)", unknown, name, known)
	}
	err := ipcReplyError("render", unknown, "future daemon")
	for _, want := range []string{"99", "protocol v1", "future daemon"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ipcReplyError(%d, ...) = %q, want it to contain %q", unknown, err, want)
		}
	}
}
