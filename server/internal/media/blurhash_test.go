package media

import (
	"encoding/base64"
	"image"
	"image/color"
	"testing"
)

// These vectors were produced by transliterating the reference BlurHash
// TypeScript implementation (github.com/woltapp/blurhash, TypeScript/src)
// to plain JavaScript — types stripped, logic untouched — and running it
// under Node on four synthetic RGBA images. See the comment on
// encodeBlurhashRGBA in blurhash.go for exactly what this does and does not
// prove. pixelsB64 is the raw, non-premultiplied RGBA byte buffer (row-major,
// top to bottom) fed to the reference encoder.
var blurhashVectors = []struct {
	name      string
	width     int
	height    int
	cx, cy    int
	pixelsB64 string
	wantHash  string
}{
	{
		name: "solid_red_4x3_3x3", width: 4, height: 3, cx: 3, cy: 3,
		pixelsB64: "/wAA//8AAP//AAD//wAA//8AAP//AAD//wAA//8AAP//AAD//wAA//8AAP//AAD/",
		wantHash:  "K~TI:j|cfQ|c$5fQfQfQfQ",
	},
	{
		name: "solid_gray_5x5_4x3", width: 5, height: 5, cx: 4, cy: 3,
		pixelsB64: "gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/4CAgP+AgID/gICA/w==",
		wantHash:  "LDEyb[~qfQ~q~qxufQxufQfQfQfQ",
	},
	{
		name: "gradient_6x4_4x3", width: 6, height: 4, cx: 4, cy: 3,
		pixelsB64: "AACA/zMAgP9mAID/mQCA/8wAgP//AID/AFWA/zNVgP9mVYD/mVWA/8xVgP//VYD/AKqA/zOqgP9mqoD/maqA/8yqgP//qoD/AP+A/zP/gP9m/4D/mf+A/8z/gP///4D/",
		wantHash:  "LSIOwK46Jl~U_eE9SMvodLeXfQeX",
	},
	{
		name: "gradient_9x9_5x5", width: 9, height: 9, cx: 5, cy: 5,
		pixelsB64: "AACA/x8AgP8/AID/XwCA/38AgP+fAID/vwCA/98AgP//AID/AB+A/x8fgP8/H4D/Xx+A/38fgP+fH4D/vx+A/98fgP//H4D/AD+A/x8/gP8/P4D/Xz+A/38/gP+fP4D/vz+A/98/gP//P4D/AF+A/x9fgP8/X4D/X1+A/39fgP+fX4D/v1+A/99fgP//X4D/AH+A/x9/gP8/f4D/X3+A/39/gP+ff4D/v3+A/99/gP//f4D/AJ+A/x+fgP8/n4D/X5+A/3+fgP+fn4D/v5+A/9+fgP//n4D/AL+A/x+/gP8/v4D/X7+A/3+/gP+fv4D/v7+A/9+/gP//v4D/AN+A/x/fgP8/34D/X9+A/3/fgP+f34D/v9+A/9/fgP//34D/AP+A/x//gP8//4D/X/+A/3//gP+f/4D/v/+A/9//gP///4D/",
		wantHash:  "eBH_;+4Qwx~o2E_e9Ojtq*N]gJfjfQfjfQ~oBmjtt6N]dLeXfQeXfQ",
	},
}

func TestEncodeBlurhashRGBA_MatchesReferencePort(t *testing.T) {
	for _, v := range blurhashVectors {
		t.Run(v.name, func(t *testing.T) {
			pixels, err := base64.StdEncoding.DecodeString(v.pixelsB64)
			if err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			got, err := encodeBlurhashRGBA(pixels, v.width, v.height, v.cx, v.cy)
			if err != nil {
				t.Fatalf("encodeBlurhashRGBA: %v", err)
			}
			if got != v.wantHash {
				t.Fatalf("hash = %q, want %q", got, v.wantHash)
			}
			// A correctly formed BlurHash string is entirely base83.
			for _, c := range got {
				if !containsRune(blurhashDigits, c) {
					t.Fatalf("hash %q contains non-base83 character %q", got, c)
				}
			}
		})
	}
}

func TestEncodeBlurhash_ImageWrapperMatchesCore(t *testing.T) {
	v := blurhashVectors[0]
	pixels, err := base64.StdEncoding.DecodeString(v.pixelsB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, v.width, v.height))
	for y := 0; y < v.height; y++ {
		for x := 0; x < v.width; x++ {
			i := (y*v.width + x) * 4
			img.SetNRGBA(x, y, color.NRGBA{R: pixels[i], G: pixels[i+1], B: pixels[i+2], A: pixels[i+3]})
		}
	}
	got, err := EncodeBlurhash(img, v.cx, v.cy)
	if err != nil {
		t.Fatalf("EncodeBlurhash: %v", err)
	}
	if got != v.wantHash {
		t.Fatalf("EncodeBlurhash via image.Image = %q, want %q (core algorithm matched but the image.Image -> RGBA byte conversion did not)", got, v.wantHash)
	}
}

func TestEncodeBlurhash_Deterministic(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 30), B: 100, A: 255})
		}
	}
	first, err := EncodeBlurhash(img, 4, 3)
	if err != nil {
		t.Fatalf("EncodeBlurhash: %v", err)
	}
	for range 5 {
		again, err := EncodeBlurhash(img, 4, 3)
		if err != nil {
			t.Fatalf("EncodeBlurhash: %v", err)
		}
		if again != first {
			t.Fatalf("EncodeBlurhash is not deterministic: got %q then %q", first, again)
		}
	}
}

func TestEncodeBlurhash_RejectsBadComponents(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if _, err := EncodeBlurhash(img, 0, 3); err == nil {
		t.Fatal("expected error for componentX=0")
	}
	if _, err := EncodeBlurhash(img, 3, 10); err == nil {
		t.Fatal("expected error for componentY=10")
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
