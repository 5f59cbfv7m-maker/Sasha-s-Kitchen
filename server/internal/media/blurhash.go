package media

import (
	"fmt"
	"image"
	"math"
)

// This file is a from-scratch Go port of the public BlurHash algorithm
// (https://github.com/woltapp/blurhash), written by hand because no BlurHash
// library is available in go.mod and adding one is out of scope here. It
// follows the reference TypeScript implementation
// (TypeScript/src/{encode,utils,base83}.ts in that repository) line for
// line — same DCT-based component extraction, same sRGB<->linear
// conversion, same base83 packing — rather than reinventing the scheme.
//
// Verification performed: the reference TypeScript was transliterated to
// plain JavaScript (types stripped only, logic untouched) and run under
// Node on four synthetic images (solid colors and gradients); this Go port
// is unit-tested to reproduce those exact hash strings byte for byte (see
// blurhash_test.go). That proves this port matches the reference algorithm
// on the cases checked. It has NOT been run against the upstream project's
// own test suite (which ships visual/manual fixtures, not machine-checkable
// vectors) or against any other independent BlurHash implementation, so
// treat it as verified-by-port-comparison rather than certified upstream.

const blurhashDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

// EncodeBlurhash computes a BlurHash string for img using componentX by
// componentY DCT components (each must be in [1,9]).
func EncodeBlurhash(img image.Image, componentX, componentY int) (string, error) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return "", fmt.Errorf("media: blurhash: empty image")
	}
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			i := (y*width + x) * 4
			// RGBA() returns 16-bit-per-channel premultiplied values; shift
			// down to 8-bit and un-premultiply so this matches a plain
			// non-premultiplied RGBA byte buffer, which is what the
			// reference algorithm assumes (canvas ImageData).
			pixels[i+0] = unpremultiply(r, a)
			pixels[i+1] = unpremultiply(g, a)
			pixels[i+2] = unpremultiply(b, a)
			pixels[i+3] = byte(a >> 8)
		}
	}
	return encodeBlurhashRGBA(pixels, width, height, componentX, componentY)
}

func unpremultiply(c, a uint32) byte {
	if a == 0 {
		return 0
	}
	v := c * 0xffff / a
	if v > 0xffff {
		v = 0xffff
	}
	return byte(v >> 8)
}

// encodeBlurhashRGBA is the core algorithm, operating directly on a
// non-premultiplied, row-major RGBA byte buffer (4 bytes per pixel, rows top
// to bottom). Kept separate from EncodeBlurhash so tests can feed it exact
// byte buffers without going through image.Image.
func encodeBlurhashRGBA(pixels []byte, width, height, componentX, componentY int) (string, error) {
	if componentX < 1 || componentX > 9 || componentY < 1 || componentY > 9 {
		return "", fmt.Errorf("media: blurhash: components must be in [1,9], got %dx%d", componentX, componentY)
	}
	if width*height*4 != len(pixels) {
		return "", fmt.Errorf("media: blurhash: pixel buffer length %d does not match %dx%d", len(pixels), width, height)
	}

	type triplet [3]float64
	factors := make([]triplet, 0, componentX*componentY)
	for y := 0; y < componentY; y++ {
		for x := 0; x < componentX; x++ {
			normalisation := 2.0
			if x == 0 && y == 0 {
				normalisation = 1.0
			}
			factors = append(factors, multiplyBasisFunction(pixels, width, height, func(i, j int) float64 {
				return normalisation *
					math.Cos(math.Pi*float64(x*i)/float64(width)) *
					math.Cos(math.Pi*float64(y*j)/float64(height))
			}))
		}
	}

	dc := factors[0]
	ac := factors[1:]

	hash := ""
	sizeFlag := (componentX - 1) + (componentY-1)*9
	hash += encode83(sizeFlag, 1)

	var maximumValue float64
	if len(ac) > 0 {
		actualMaximumValue := math.Inf(-1)
		for _, v := range ac {
			m := math.Max(v[0], math.Max(v[1], v[2]))
			if m > actualMaximumValue {
				actualMaximumValue = m
			}
		}
		quantised := int(math.Floor(clamp(math.Floor(actualMaximumValue*166-0.5), 0, 82)))
		maximumValue = float64(quantised+1) / 166
		hash += encode83(quantised, 1)
	} else {
		maximumValue = 1
		hash += encode83(0, 1)
	}

	hash += encode83(encodeDC(dc), 4)
	for _, factor := range ac {
		hash += encode83(encodeAC(factor, maximumValue), 2)
	}
	return hash, nil
}

func multiplyBasisFunction(pixels []byte, width, height int, basis func(i, j int) float64) [3]float64 {
	var r, g, b float64
	bytesPerRow := width * 4
	for x := 0; x < width; x++ {
		xOff := 4 * x
		for y := 0; y < height; y++ {
			idx := xOff + y*bytesPerRow
			bf := basis(x, y)
			r += bf * sRGBToLinear(pixels[idx])
			g += bf * sRGBToLinear(pixels[idx+1])
			b += bf * sRGBToLinear(pixels[idx+2])
		}
	}
	scale := 1.0 / float64(width*height)
	return [3]float64{r * scale, g * scale, b * scale}
}

func sRGBToLinear(value byte) float64 {
	v := float64(value) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(value float64) int {
	v := clamp(value, 0, 1)
	if v <= 0.0031308 {
		return int(v*12.92*255 + 0.5) // truncation toward zero == floor for v >= 0
	}
	return int((1.055*math.Pow(v, 1.0/2.4)-0.055)*255 + 0.5)
}

func signPow(val, exp float64) float64 {
	s := 1.0
	if val < 0 {
		s = -1.0
	}
	return s * math.Pow(math.Abs(val), exp)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func encodeDC(value [3]float64) int {
	r := linearToSRGB(value[0])
	g := linearToSRGB(value[1])
	b := linearToSRGB(value[2])
	return (r << 16) + (g << 8) + b
}

func encodeAC(value [3]float64, maximumValue float64) int {
	quant := func(v float64) int {
		q := math.Floor(signPow(v/maximumValue, 0.5)*9 + 9.5)
		return int(clamp(q, 0, 18))
	}
	qr, qg, qb := quant(value[0]), quant(value[1]), quant(value[2])
	return qr*19*19 + qg*19 + qb
}

var base83Powers = [5]int{1, 83, 83 * 83, 83 * 83 * 83, 83 * 83 * 83 * 83}

func encode83(n, length int) string {
	buf := make([]byte, length)
	for i := 1; i <= length; i++ {
		digit := (n / base83Powers[length-i]) % 83
		buf[i-1] = blurhashDigits[digit]
	}
	return string(buf)
}
