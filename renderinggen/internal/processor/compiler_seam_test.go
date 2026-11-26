package processor

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// stubSemanticCompiler is the consumer-side test double for the compile seam.
type stubSemanticCompiler struct {
	called        bool
	resolveCalled bool
	result        overlay.CompileResult
	err           error
}

func (s *stubSemanticCompiler) CompileSemantic([]byte) (overlay.CompileResult, error) {
	s.called = true
	return s.result, s.err
}

func (s *stubSemanticCompiler) ResolveSemanticForPrepare([]byte) (*overlay.PreparedResolve, error) {
	s.resolveCalled = true
	return nil, s.err
}

// TestCompileSemanticUsesInjectedCompiler pins the consumer-side seam: the
// one-shot compile must dispatch through Processor.compileSemantic, so an
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

// TestResolveSemanticForPrepareUsesTheSeam pins the prepare dispatch: the
// resolve half of the split compile goes through the injected compiler, so the
// prepare path can be driven without the real lowering.
func TestResolveSemanticForPrepareUsesTheSeam(t *testing.T) {
	stub := &stubSemanticCompiler{err: errors.New("resolve stub")}
	p := &Processor{compiler: stub}
	if _, err := p.resolveSemanticForPrepare([]byte(`{}`)); err == nil {
		t.Fatal("resolve seam did not surface its error")
	}
	if !stub.resolveCalled || stub.called {
		t.Fatalf("resolve seam called=%t one-shot called=%t, want resolve only", stub.resolveCalled, stub.called)
	}
}

// TestResolveSemanticForPrepareFallsBackToOverlayCompiler pins the nil branch of
// the prepare seam: a struct-literal Processor resolves with the real compiler.
func TestResolveSemanticForPrepareFallsBackToOverlayCompiler(t *testing.T) {
	p := &Processor{}
	if _, err := p.resolveSemanticForPrepare([]byte(`not json`)); err == nil {
		t.Fatal("zero-value Processor must still resolve through the real compiler and reject invalid JSON")
	}
}
