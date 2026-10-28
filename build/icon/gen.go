//go:build ignore

// Icon generator for Loom macOS app and menu bar template icon, sourced from build/icon/loom2.png.
//
//	go run build/icon/gen.go tray internal/gui/tray.png
//	go run build/icon/gen.go app 1024 internal/gui/icon-1024.png
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
)

//go:embed loom2.png
var loomPNG []byte

func main() {
	srcImg, err := png.Decode(bytes.NewReader(loomPNG))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var img image.Image
	switch {
	case len(os.Args) == 3 && os.Args[1] == "tray":
		img = drawTrayFromSource(srcImg, 44)
	case len(os.Args) == 4 && os.Args[1] == "app":
		n, _ := strconv.Atoi(os.Args[2])
		img = drawAppFromSource(srcImg, n)
	default:
		fmt.Fprintln(os.Stderr, "usage: gen.go tray <out.png> | gen.go app <size> <out.png>")
		os.Exit(2)
	}
	f, err := os.Create(os.Args[len(os.Args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// drawAppFromSource scales the source 1024x1024 icon into target size with
// standard macOS Big Sur+ Dock icon margin (~81% tile within the canvas).
func drawAppFromSource(src image.Image, size int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	const tileScale = 0.82
	pad := float64(size) * (1.0 - tileScale) / 2.0
	tileSize := float64(size) * tileScale

	// Supersampled 2x2 box filter for crisp downscaling
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var rSum, gSum, bSum, aSum float64
			const samples = 2
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/float64(samples)
					py := float64(y) + (float64(sy)+0.5)/float64(samples)
					u := (px - pad) / tileSize
					v := (py - pad) / tileSize
					if u < 0 || u >= 1 || v < 0 || v >= 1 {
						continue
					}
					srcX := b.Min.X + int(u*sw)
					srcY := b.Min.Y + int(v*sh)
					cr, cg, cb, ca := src.At(srcX, srcY).RGBA()
					a := float64(ca) / 65535.0
					if a > 0 {
						// Un-premultiply
						rSum += (float64(cr) / 65535.0 / a) * a
						gSum += (float64(cg) / 65535.0 / a) * a
						bSum += (float64(cb) / 65535.0 / a) * a
						aSum += a
					}
				}
			}
			total := float64(samples * samples)
			alpha := aSum / total
			if alpha > 0 {
				dst.SetNRGBA(x, y, color.NRGBA{
					R: uint8(math.Min(255, (rSum/aSum)*255.0+0.5)),
					G: uint8(math.Min(255, (gSum/aSum)*255.0+0.5)),
					B: uint8(math.Min(255, (bSum/aSum)*255.0+0.5)),
					A: uint8(math.Min(255, alpha*255.0+0.5)),
				})
			}
		}
	}
	return dst
}

// drawTrayFromSource extracts the central woven dark glyph from loom2.png as a
// black + alpha template icon for the macOS menu bar.
func drawTrayFromSource(src image.Image, size int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())

	// Crop the central glyph region (0.16..0.84 of the source image) and fit into 80% of the tray square
	const cropMin = 0.16
	const cropSpan = 0.68
	const trayScale = 0.82
	pad := float64(size) * (1.0 - trayScale) / 2.0
	box := float64(size) * trayScale

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var cov float64
			const samples = 4
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/float64(samples)
					py := float64(y) + (float64(sy)+0.5)/float64(samples)
					u := (px - pad) / box
					v := (py - pad) / box
					if u < 0 || u >= 1 || v < 0 || v >= 1 {
						continue
					}
					srcX := b.Min.X + int((cropMin+u*cropSpan)*sw)
					srcY := b.Min.Y + int((cropMin+v*cropSpan)*sh)
					cr, cg, cb, ca := src.At(srcX, srcY).RGBA()
					if ca < 32768 {
						continue
					}
					// Dark glyph has low red/green/blue compared to terracotta background
					lum := (0.299*float64(cr) + 0.587*float64(cg) + 0.114*float64(cb)) / 65535.0
					if lum < 0.35 {
						strength := math.Min(1.0, (0.35-lum)/0.22)
						cov += strength
					}
				}
			}
			alpha := cov / float64(samples*samples)
			if alpha > 0.02 {
				dst.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(math.Min(255, alpha*255.0+0.5))})
			}
		}
	}
	return dst
}
