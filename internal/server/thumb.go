package server

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	thumbMaxEdge = 480
	fullMaxEdge  = 1600
)

// deriveThumb maps a stored public full path to its thumbnail path.
func deriveThumb(p string) string {
	if p == "" || !strings.HasPrefix(p, "/uploads/") {
		return ""
	}
	dot := strings.LastIndexByte(p, '.')
	if dot < 0 || dot < strings.LastIndexByte(p, '/') {
		return ""
	}
	return p[:dot] + "_thumb" + p[dot:]
}

func downscaleBox(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxEdge <= 0 || (w <= maxEdge && h <= maxEdge) {
		return src
	}
	scale := float64(maxEdge) / float64(w)
	if h > w {
		scale = float64(maxEdge) / float64(h)
	}
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 { nw = 1 }
	if nh < 1 { nh = 1 }
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0 := y * h / nh
		y1 := (y + 1) * h / nh
		if y1 <= y0 { y1 = y0 + 1 }
		if y1 > h { y1 = h }
		for x := 0; x < nw; x++ {
			x0 := x * w / nw
			x1 := (x + 1) * w / nw
			if x1 <= x0 { x1 = x0 + 1 }
			if x1 > w { x1 = w }
			var r, g, bl, a uint64
			n := 0
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r += uint64(pr); g += uint64(pg); bl += uint64(pb); a += uint64(pa)
					n++
				}
			}
			if n == 0 { n = 1 }
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / uint64(n) >> 8),
				G: uint8(g / uint64(n) >> 8),
				B: uint8(bl / uint64(n) >> 8),
				A: uint8(a / uint64(n) >> 8),
			})
		}
	}
	return dst
}

func encodeScaled(w io.Writer, img image.Image, ext string, maxEdge, quality int) error {
	if maxEdge > 0 {
		img = downscaleBox(img, maxEdge)
	}
	if ext == ".png" {
		return png.Encode(w, img)
	}
	return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
}

// EnsureThumbOnDisk creates <name>_thumb.<ext> next to fullPath if missing.
func EnsureThumbOnDisk(fullPath string) (bool, error) {
	ext := filepath.Ext(fullPath)
	lo := strings.ToLower(ext)
	if lo != ".jpg" && lo != ".jpeg" && lo != ".png" {
		return false, nil
	}
	thumb := strings.TrimSuffix(fullPath, ext) + "_thumb" + ext
	if _, err := os.Stat(thumb); err == nil {
		return false, nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return false, err
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return false, err
	}
	tmp := thumb + ".tmp"
	dst, err := os.Create(tmp)
	if err != nil {
		return false, err
	}
	if err := encodeScaled(dst, img, lo, thumbMaxEdge, 82); err != nil {
		dst.Close()
		os.Remove(tmp)
		return false, err
	}
	dst.Close()
	if err := os.Rename(tmp, thumb); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}
