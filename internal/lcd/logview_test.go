package lcd

import "testing"

// TestLogViewPan checks the horizontal pan math + clamping (Draw sets the
// geometry; here we set it directly and drive Key).
func TestLogViewPan(t *testing.T) {
	lv := &LogView{cols: 10, maxLen: 30, visible: 5, total: 3} // as if Draw ran
	// step = 2; maxLeft = maxLen-cols = 20.
	lv.Key(KeyRight)
	if lv.left != 2 {
		t.Fatalf("after one Right, left = %d, want 2", lv.left)
	}
	for i := 0; i < 20; i++ {
		lv.Key(KeyRight) // should clamp at maxLeft
	}
	if lv.left != 20 {
		t.Fatalf("left clamped at %d, want 20", lv.left)
	}
	lv.Key(KeyLeft)
	if lv.left != 18 {
		t.Fatalf("after Left, left = %d, want 18", lv.left)
	}
	for i := 0; i < 10; i++ {
		lv.Key(KeyLeft) // clamp at 0
	}
	if lv.left != 0 {
		t.Fatalf("left clamped at %d, want 0", lv.left)
	}
	// vertical still works (total 3, visible 5 → no scroll room)
	lv.Key(KeyDown)
	if lv.top != 0 {
		t.Fatalf("top = %d, want 0 (no overflow)", lv.top)
	}
}
