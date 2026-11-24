// Package prescale owns the pure image-derivative policy used by the processor:
// display-bound sizing, area-average resizing, JPEG/PNG encoding, cache paths
// and atomic installation. It has no processor or render-plan dependency.
package prescale

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// Extension returns the file extension for a supported source format.
func Extension(format string) string {
	if format == "jpeg" {
		return ".jpg"
	}
	return ".png"
}

// CachePath derives the deterministic cache path for a source hash, bound and
// format.
func CachePath(cacheDir, sourceHash string, maxDimension int, format string) string {
	key := storage.Hash([]byte(sourceHash + "\x00" + strconv.Itoa(maxDimension) + "\x00" + format))
	return filepath.Join(cacheDir, key[:2], key+Extension(format))
}

// LoadCache returns cached bytes only when their decoded dimensions match the
// requested derivative. A corrupt or stale cache entry is a miss.
func LoadCache(cachePath string, width, height int) ([]byte, bool) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != width || config.Height != height {
		return nil, false
	}
	return data, true
}

// WriteAtomicFile installs data through an exclusive temp file in the
// destination directory. Temp cleanup is best-effort: the write/chmod/rename
// error explains why the file was not installed.
func WriteAtomicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".prescale-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

// DisplayBoundSize fits width/height inside maxDimension, preserving aspect
// ratio and never producing a zero dimension.
func DisplayBoundSize(width, height, maxDimension int) (int, int) {
	if width <= maxDimension && height <= maxDimension {
		return width, height
	}
	if width >= height {
		target := maxDimension
		scaled := (height*maxDimension + width/2) / width
		if scaled < 1 {
			scaled = 1
		}
		return target, scaled
	}
	target := maxDimension
	scaled := (width*maxDimension + height/2) / height
	if scaled < 1 {
		scaled = 1
	}
	return scaled, target
}

// ResizeAreaAverage is a deterministic box filter over premultiplied RGBA.
// Averaging premultiplied channels avoids the dark fringe that averaging
// straight-alpha channels produces on transparent edges.
func ResizeAreaAverage(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0 := bounds.Min.Y + y*sourceHeight/height
		y1 := bounds.Min.Y + (y+1)*sourceHeight/height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0 := bounds.Min.X + x*sourceWidth/width
			x1 := bounds.Min.X + (x+1)*sourceWidth/width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha uint64
			for sourceY := y0; sourceY < y1; sourceY++ {
				for sourceX := x0; sourceX < x1; sourceX++ {
					r, g, b, a := source.At(sourceX, sourceY).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
				}
			}
			count := uint64((x1 - x0) * (y1 - y0))
			red /= count
			green /= count
			blue /= count
			alpha /= count
			out := destination.PixOffset(x, y)
			if alpha == 0 {
				destination.Pix[out+3] = 0
				continue
			}
			destination.Pix[out] = uint8((red * 0xffff / alpha) >> 8)
			destination.Pix[out+1] = uint8((green * 0xffff / alpha) >> 8)
			destination.Pix[out+2] = uint8((blue * 0xffff / alpha) >> 8)
			destination.Pix[out+3] = uint8(alpha >> 8)
		}
	}
	return destination
}

// Encode serializes a derivative as JPEG (quality 90) or PNG (default
// compression).
func Encode(source image.Image, format string) ([]byte, string, error) {
	var buffer bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buffer, source, &jpeg.Options{Quality: 90}); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), ".jpg", nil
	case "png":
		encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := encoder.Encode(&buffer, source); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), ".png", nil
	default:
		return nil, "", fmt.Errorf("unsupported image format %q", format)
	}
}
