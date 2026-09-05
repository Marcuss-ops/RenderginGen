package certification

import "testing"

func TestMemoryGateRejectsFrameTransientLeak(t *testing.T) {
	if err := MemoryGate([]FrameMetrics{{Frame: 1, FrameTransientLive: 1}}, -1); err == nil {
		t.Fatal("expected FrameTransient leak to fail")
	}
}

func TestMemoryGateRejectsLinearVRAMGrowth(t *testing.T) {
	if err := MemoryGate([]FrameMetrics{{VRAMBytes: 10}, {VRAMBytes: 1011}}, 100); err == nil {
		t.Fatal("expected linear VRAM growth to fail")
	}
}

func TestMemoryGateAcceptsReclaimedFrames(t *testing.T) {
	if err := MemoryGate([]FrameMetrics{{VRAMBytes: 1000}, {VRAMBytes: 1010}, {VRAMBytes: 1005}}, 100); err != nil {
		t.Fatal(err)
	}
}
