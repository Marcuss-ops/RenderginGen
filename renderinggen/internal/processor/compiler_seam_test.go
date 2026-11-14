package processor

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

type stubSemanticCompiler struct {
	called bool
	result overlay.CompileResult
	err    error
}

func (s *stubSemanticCompiler) CompileSemantic([]byte) (overlay.CompileResult, error) {
	s.called = true
	return s.result, s.err
}

// TestCompileSemanticUsesInjectedCompiler pins the consumer-side seam: the
// prepare path must dispatch through Processor.compileSemantic, so an
// orchestration test can drive it without the real lowering.
func TestCompileSemanticUsesInjectedCompiler(t *testing.T) {
	want := errors.New("stub compile failure")
	stub := &stubSemanticCompiler{err: want}
	p := &Processor{compiler: stub}
	_, err := p.compileSemantic([]byte(`{}`))
	if !stub.called || !errors.Is(err, want) {
		t.Fatalf("injected compiler called=%t err=%v, want called with its error", stub.called, err)
	}
}

// TestCompileSemanticFallsBackToOverlayCompiler pins the nil branch: tests that
// build a Processor as a struct literal must still get the real compiler.
func TestCompileSemanticFallsBackToOverlayCompiler(t *testing.T) {
	p := &Processor{}
	if _, err := p.compileSemantic([]byte(`not json`)); err == nil {
		t.Fatal("zero-value Processor must still dispatch to the real compiler and reject invalid JSON")
	}
}
