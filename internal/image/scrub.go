// Package image implements the listing-picture pipeline for vendor product
// images (TASKS.md Phase 4): strict decode, EXIF/GPS scrubbing by re-encode,
// and deterministic filename derivation.
package image

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	stdimage "image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/tiff" // registers the TIFF decoder for stdimage.Decode
)

// MaxInputBytes bounds the raw upload we are willing to look at. The handler
// clamps first (http.MaxBytesReader); this is the second gate so no caller
// can forget it. ~10 MB covers real camera JPEGs while staying far below any
// decompression-bomb comfort zone once combined with MaxPixels.
const MaxInputBytes = 10 << 20

// MaxPixels caps decoded dimensions (width*height) before allocation, which
// is what actually protects memory: a 30k×30k JPEG header decodes lazily and
// only explodes when fully drawn. 50 MP is generous for listing photos.
const MaxPixels = 50_000_000

// ErrUnsupported marks bytes that are not one of the accepted picture types
// (JPEG, PNG, GIF, TIFF). Handlers map it to a user-facing rejection; the
// original error detail never leaves the process (AGENTS.md: generic errors).
var ErrUnsupported = errors.New("image: unsupported or malformed image")

// Scrubbed is one sanitized listing picture: the re-encoded bytes plus the
// canonical extension to serve them under.
type Scrubbed struct {
	Data []byte
	Ext  string // ".jpg", ".png", or ".gif" — never derived from client input
}

// Scrub decodes data as an image and re-encodes it so EVERY metadata channel
// dies on the way out: EXIF (GPS coordinates, camera serial, thumbnails,
// owner name), ICC profile strings, comments, timestamps. This is why the
// onion-shop listing pipeline refuses to simply store the upload — a JPEG's
// EXIF block can pin a vendor to a physical location.
//
// Format policy:
//   - JPEG → re-encode JPEG (quality 80). Lossy→lossy, acceptable for photos.
//   - PNG  → re-encode PNG via opaque RGBA draw: tRNS/gAMA/tEXt/iTXt/zTXt all
//     dropped by construction (only pixels survive the Draw).
//   - GIF  → keep GIF but write WITHOUT the Comment extension
//     (golang.org/x/image writes none unless asked). Animated GIFs collapse
//     to their first frame — fine for static listing pictures.
//   - TIFF → decode only; always re-encoded as PNG (TIFF carries heavy EXIF).
func Scrub(data []byte) (*Scrubbed, error) {
	if len(data) == 0 || len(data) > MaxInputBytes {
		return nil, fmt.Errorf("%w: size %d", ErrUnsupported, len(data))
	}
	cfg, _, err := stdimage.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: config: %v", ErrUnsupported, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return nil, fmt.Errorf("%w: dimensions %dx%d", ErrUnsupported, cfg.Width, cfg.Height)
	}
	img, format, err := stdimage.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrUnsupported, err)
	}

	var out bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
			return nil, fmt.Errorf("image: re-encode jpeg: %w", err)
		}
		return &Scrubbed{Data: out.Bytes(), Ext: ".jpg"}, nil
	case "png":
		if err := png.Encode(&out, opaque(img)); err != nil {
			return nil, fmt.Errorf("image: re-encode png: %w", err)
		}
		return &Scrubbed{Data: out.Bytes(), Ext: ".png"}, nil
	case "gif":
		if err := gif.Encode(&out, img, nil); err != nil {
			return nil, fmt.Errorf("image: re-encode gif: %w", err)
		}
		return &Scrubbed{Data: out.Bytes(), Ext: ".gif"}, nil
	case "tiff":
		if err := png.Encode(&out, opaque(img)); err != nil {
			return nil, fmt.Errorf("image: re-encode tiff->png: %w", err)
		}
		return &Scrubbed{Data: out.Bytes(), Ext: ".png"}, nil
	default:
		return nil, fmt.Errorf("%w: format %q", ErrUnsupported, format)
	}
}

// opaque draws src onto a fresh NRGBA canvas so palette transparency (tRNS)
// and any non-pixel ancillary chunks cannot reach the encoder — the output
// PNG contains IDAT + minimal control chunks only. draw.Over with an empty
// source rect paints fully-transparent black underneath first, flattening
// every alpha channel into the pixels themselves.
func opaque(src stdimage.Image) *stdimage.NRGBA {
	b := src.Bounds()
	dst := stdimage.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// Name derives the stored object name from the scrubbed bytes: hex(SHA-256)
// of the final content plus the server-chosen extension. Content-addressing
// means a replace is just a new name (old file deleted by the store layer),
// and the name is provably free of any client-supplied path component.
func Name(scrubbed []byte) string {
	sum := sha256.Sum256(scrubbed)
	return hex.EncodeToString(sum[:])
}

// SanitizeExt validates a server-generated extension against the allowlist.
// Defense in depth for names read back from the DB; "" passes through.
func SanitizeExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".png":
		return ".png"
	case ".gif":
		return ".gif"
	case "":
		return ""
	default:
		return ""
	}
}

// Base strips any directory components from a stored path before serving, so
// even a corrupted DB row ("/../../etc/passwd.png") can only ever resolve to
// a bare filename inside the media root.
func Base(p string) string {
	return filepath.Base(filepath.Clean("/" + p))[1:] // trim leading "/" added by Clean
}

// ReadAllLimited reads up to max bytes and errors past that bound — used by
// handlers so io.ReadAll can never be walked into OOM by a chunked upload.
func ReadAllLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrUnsupported, max)
	}
	return data, nil
}
