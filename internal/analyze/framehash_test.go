package analyze

import (
	"image"
	"image/color"
	"testing"
)

func grayPattern(f func(x, y int) uint8) image.Image {
	im := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			im.SetGray(x, y, color.Gray{Y: f(x, y)})
		}
	}
	return im
}

func TestAverageHashDedup(t *testing.T) {
	// A：左黑右白（竖分界 x=32）
	a := averageHash(grayPattern(func(x, y int) uint8 {
		if x < 32 {
			return 0
		}
		return 255
	}))
	// B：分界略移到 x=30 —— 与 A 近乎相同
	b := averageHash(grayPattern(func(x, y int) uint8 {
		if x < 30 {
			return 0
		}
		return 255
	}))
	// C：上黑下白（横分界）—— 与 A 布局完全不同
	c := averageHash(grayPattern(func(x, y int) uint8 {
		if y < 32 {
			return 0
		}
		return 255
	}))

	dAB := hamming(a, b)
	dAC := hamming(a, c)
	if dAB > 4 {
		t.Errorf("near-duplicate hamming(A,B) = %d, want small (<=4)", dAB)
	}
	if dAC <= dAB {
		t.Errorf("different layout hamming(A,C)=%d should exceed hamming(A,B)=%d", dAC, dAB)
	}
	if hamming(a, a) != 0 {
		t.Errorf("hamming(A,A) must be 0")
	}
}
