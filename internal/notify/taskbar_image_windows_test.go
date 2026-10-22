package notify

import (
	"image/color"
	"testing"
)

func TestTaskbarAttentionImageHasRedBackgroundAndWhiteExclamation(t *testing.T) {
	img := badgeImage(taskbarStatus{attention: true})
	if got := img.RGBAAt(10, 10); got != (color.RGBA{220, 38, 38, 255}) {
		t.Fatal("warning background is not red", got)
	}
	for _, point := range [][2]int{{15, 6}, {15, 24}} {
		if got := img.RGBAAt(point[0], point[1]); got != (color.RGBA{255, 255, 255, 255}) {
			t.Fatal("missing exclamation glyph", point, got)
		}
	}
	if img.RGBAAt(0, 0).A != 0 {
		t.Fatal("icon corners are not transparent")
	}
	if got := badgeImage(taskbarStatus{count: 2}).RGBAAt(10, 10); got == img.RGBAAt(10, 10) {
		t.Fatal("completion and attention colors must differ")
	}
}
