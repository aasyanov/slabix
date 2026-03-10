package align

import (
	"testing"
)

func TestOf(t *testing.T) {
	tests := []struct {
		n, align, want uintptr
	}{
		{0, 8, 0},
		{1, 8, 8},
		{7, 8, 8},
		{8, 8, 8},
		{9, 8, 16},
		{0, 64, 0},
		{1, 64, 64},
		{63, 64, 64},
		{64, 64, 64},
		{65, 64, 128},
	}
	for _, tt := range tests {
		got := Of(tt.n, tt.align)
		if got != tt.want {
			t.Errorf("Of(%d, %d) = %d, want %d", tt.n, tt.align, got, tt.want)
		}
	}
}

func TestSizeOf(t *testing.T) {
	type s32 struct{ _, _, _, _ int64 }

	got := SizeOf[s32](64)
	if got < 32 {
		t.Fatalf("SizeOf[s32](64) = %d, want >= 32", got)
	}
	if got%64 != 0 {
		t.Fatalf("SizeOf[s32](64) = %d, not aligned to 64", got)
	}
}

func TestIsPowerOfTwo(t *testing.T) {
	tests := []struct {
		n    uintptr
		want bool
	}{
		{0, false},
		{1, true},
		{2, true},
		{3, false},
		{4, true},
		{64, true},
		{100, false},
		{1024, true},
	}
	for _, tt := range tests {
		got := IsPowerOfTwo(tt.n)
		if got != tt.want {
			t.Errorf("IsPowerOfTwo(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}
