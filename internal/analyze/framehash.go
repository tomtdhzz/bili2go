package analyze

import (
	"image"
	"image/jpeg"
	"math/bits"
	"os"
)

// averageHash 计算 8x8 灰度均值感知哈希（aHash）：把图缩到 8x8 灰度，
// 每格灰度大于全图均值记 1。近乎相同的画面哈希也相近，用于跳过静态帧。
func averageHash(img image.Image) uint64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return 0
	}
	var gray [64]uint32
	var sum uint32
	for gy := range 8 {
		for gx := range 8 {
			// 最近采样：取每格中心像素
			px := b.Min.X + (gx*2+1)*w/16
			py := b.Min.Y + (gy*2+1)*h/16
			r, g, bl, _ := img.At(px, py).RGBA()
			// 亮度（0..255）
			lum := (299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000
			gray[gy*8+gx] = lum
			sum += lum
		}
	}
	mean := sum / 64
	var hash uint64
	for i := range 64 {
		if gray[i] > mean {
			hash |= 1 << uint(i)
		}
	}
	return hash
}

// hamming 是两个哈希的汉明距离（不同位数）。
func hamming(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// frameAHash 读一张 jpg 帧算 aHash。
func frameAHash(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return 0, err
	}
	return averageHash(img), nil
}
