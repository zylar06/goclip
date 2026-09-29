package media

import (
	"bytes"
	"crypto/sha256"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"autoclip-go/internal/domain"
)

func TestTitleAllStylesDeterministicAndTransparent(t *testing.T) {
	tools := New(Config{})
	hashes := map[[32]byte]string{}
	for _, style := range domain.Styles {
		t.Run(style, func(t *testing.T) {
			d := testDraft()
			d.TitleStyle, d.Hook = style, "Hello 世界\n日本語"
			data, err := tools.TitlePNG(d, 480, 270)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() != 480 || img.Bounds().Dy() != 270 {
				t.Fatalf("invalid PNG: %v", err)
			}
			_, _, _, alpha := img.At(0, 0).RGBA()
			if alpha != 0 {
				t.Fatal("canvas must have a transparent background")
			}
			visible := 0
			for y := 0; y < 270; y++ {
				for x := 0; x < 480; x++ {
					_, _, _, a := img.At(x, y).RGBA()
					if a > 0 {
						visible++
					}
				}
			}
			if visible < 100 {
				t.Fatal("empty artwork")
			}
			again, err := tools.TitlePNG(d, 480, 270)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatal("title must be deterministic")
			}
			hash := sha256.Sum256(data)
			if prior, ok := hashes[hash]; ok {
				t.Fatalf("%s and %s have identical artwork", prior, style)
			}
			hashes[hash] = style
		})
	}
}

func TestTitleValidationAndFonts(t *testing.T) {
	tools := New(Config{})
	d := testDraft()
	for _, modify := range []func(*domain.Draft){
		func(d *domain.Draft) { d.TitleStyle = "unknown" },
		func(d *domain.Draft) { d.TitleScale = math.NaN() },
		func(d *domain.Draft) { d.TitleY = 1 },
		func(d *domain.Draft) { d.TitleAccent = ptr("#zzzzzz") },
		func(d *domain.Draft) { d.Title = "" },
		func(d *domain.Draft) { d.Title = "\U0010FFFF" },
	} {
		draft := d
		modify(&draft)
		if _, err := tools.TitlePNG(draft, 320, 180); err == nil {
			t.Fatal("invalid title accepted")
		}
	}
	if _, err := tools.TitlePNG(d, 8, 8); err == nil {
		t.Fatal("tiny canvas accepted")
	}
	if _, err := tools.TitlePNG(d, 8192, 8192); err == nil {
		t.Fatal("oversize canvas accepted")
	}
	if _, err := New(Config{FontDir: t.TempDir()}).TitlePNG(d, 320, 180); err == nil {
		t.Fatal("missing font silently replaced")
	}
	static, err := os.ReadFile(filepath.Join(tools.cfg.FontDir, "NotoSansSC-StaticBold.ttf"))
	if err != nil || variableFont(static) {
		t.Fatalf("static font missing or variable: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(tools.cfg.FontDir, "NotoSansSC.ttf"))
	if err != nil || !variableFont(original) {
		t.Fatalf("upstream variable font check failed: %v", err)
	}
	if _, err := tools.loadFont("NotoSansSC.ttf"); err == nil {
		t.Fatal("variable font should be explicitly rejected")
	}
}

func TestTitlePlacementAccentHookAndConcurrentUse(t *testing.T) {
	tools := New(Config{})
	d := testDraft()
	a, err := tools.TitlePNG(d, 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	d.TitleY, d.TitleAccent, d.Hook = .6, ptr("#33aaff"), "Hook"
	b, err := tools.TitlePNG(d, 320, 180)
	if err != nil || bytes.Equal(a, b) {
		t.Fatal("placement/accent/hook must affect PNG")
	}
	for i := 0; i < 4; i++ {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			if _, err := tools.TitlePNG(d, 320, 180); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func ptr(s string) *string { return &s }
