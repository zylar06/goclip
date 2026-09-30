package media

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"autoclip-go/internal/domain"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

type titleTheme struct {
	font                string
	text, accent, panel color.NRGBA
	outline, shadow     int
}

var white = color.NRGBA{255, 255, 255, 255}
var ink = color.NRGBA{16, 21, 30, 255}

func themeFor(style string) titleTheme {
	theme := titleTheme{font: "BarlowCondensed-Black.ttf", text: white, accent: color.NRGBA{255, 218, 40, 255}, outline: 1}
	switch style {
	case "impact":
		theme.font, theme.text, theme.outline, theme.shadow = "Anton-Regular.ttf", theme.accent, 3, 3
	case "card":
		theme.font, theme.text, theme.panel = "NotoSansSC-StaticBold.ttf", ink, color.NRGBA{248, 248, 241, 245}
		theme.outline = 0
	case "comic":
		theme.font, theme.text, theme.outline, theme.shadow = "BarlowCondensed-BlackItalic.ttf", theme.accent, 3, 5
	case "neon":
		theme.accent, theme.panel, theme.outline = color.NRGBA{0, 240, 210, 255}, color.NRGBA{9, 13, 31, 195}, 2
	case "arena":
		theme.font, theme.accent, theme.panel = "BarlowCondensed-BlackItalic.ttf", color.NRGBA{255, 65, 62, 255}, color.NRGBA{14, 24, 44, 240}
		theme.shadow = 2
	case "editorial":
		theme.font, theme.accent, theme.panel = "NotoSansSC-StaticBold.ttf", color.NRGBA{255, 72, 38, 255}, color.NRGBA{15, 18, 23, 220}
		theme.outline = 0
	case "pixel":
		theme.font, theme.accent, theme.panel, theme.outline, theme.shadow = "PressStart2P-Regular.ttf", color.NRGBA{237, 50, 124, 255}, color.NRGBA{20, 16, 49, 240}, 0, 3
	case "frosted":
		theme.font, theme.accent, theme.panel, theme.outline = "NotoSansSC-StaticBold.ttf", color.NRGBA{0, 230, 220, 255}, color.NRGBA{210, 227, 239, 155}, 1
	}
	return theme
}

func variableFont(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	count := int(binary.BigEndian.Uint16(data[4:6]))
	for i := 0; i < count && 12+16*(i+1) <= len(data); i++ {
		tag := string(data[12+16*i : 16+16*i])
		if tag == "fvar" || tag == "CFF2" {
			return true
		}
	}
	return false
}

func (t *Tools) loadFont(name string) (*opentype.Font, error) {
	t.fontMu.Lock()
	defer t.fontMu.Unlock()
	if f := t.fonts[name]; f != nil {
		return f, nil
	}
	path := filepath.Join(t.cfg.FontDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("title font %s: %w", name, err)
	}
	if variableFont(data) {
		return nil, fmt.Errorf("title font %s is variable; install the licensed static font asset", name)
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse title font %s: %w", name, err)
	}
	t.fonts[name] = f
	return f, nil
}

type titleFaces struct {
	latin, cjk         font.Face
	latinFont, cjkFont *opentype.Font
	buffer             sfnt.Buffer
}

func (f *titleFaces) close() error { return errors.Join(f.latin.Close(), f.cjk.Close()) }

func (f *titleFaces) face(r rune) (font.Face, error) {
	for _, candidate := range []struct {
		font *opentype.Font
		face font.Face
	}{{f.latinFont, f.latin}, {f.cjkFont, f.cjk}} {
		index, err := candidate.font.GlyphIndex(&f.buffer, r)
		if err != nil {
			return nil, fmt.Errorf("title glyph %U: %w", r, err)
		}
		if index != 0 {
			return candidate.face, nil
		}
	}
	return nil, fmt.Errorf("title fonts have no glyph for %U", r)
}

func (t *Tools) titleFaces(name string, size float64) (*titleFaces, error) {
	latin, err := t.loadFont(name)
	if err != nil {
		return nil, err
	}
	cjk, err := t.loadFont("NotoSansSC-StaticBold.ttf")
	if err != nil {
		return nil, err
	}
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	lf, err := opentype.NewFace(latin, opts)
	if err != nil {
		return nil, err
	}
	cf, err := opentype.NewFace(cjk, opts)
	if err != nil {
		return nil, errors.Join(err, lf.Close())
	}
	return &titleFaces{latin: lf, cjk: cf, latinFont: latin, cjkFont: cjk}, nil
}

func (f *titleFaces) measure(text string) (fixed.Int26_6, error) {
	var x fixed.Int26_6
	var prev rune
	var last font.Face
	for _, r := range text {
		face, err := f.face(r)
		if err != nil {
			return 0, err
		}
		if last == face {
			x += face.Kern(prev, r)
		}
		advance, ok := face.GlyphAdvance(r)
		if !ok {
			return 0, fmt.Errorf("title glyph metrics missing for %U", r)
		}
		x += advance
		prev, last = r, face
	}
	return x, nil
}

// Latin words are kept together when possible; CJK is wrapped by glyph.
func titleTokens(s string) []string {
	var result []string
	word := ""
	flush := func() {
		if word != "" {
			result = append(result, word)
			word = ""
		}
	}
	for _, r := range s {
		if r < 0x250 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'') {
			word += string(r)
		} else {
			flush()
			result = append(result, string(r))
		}
	}
	flush()
	return result
}

func wrapTitle(f *titleFaces, text string, maxWidth int) ([]string, error) {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		line := ""
		for _, token := range titleTokens(paragraph) {
			width, err := f.measure(line + token)
			if err != nil {
				return nil, err
			}
			if line != "" && width.Ceil() > maxWidth {
				lines = append(lines, strings.TrimSpace(line))
				line = ""
				token = strings.TrimLeftFunc(token, unicode.IsSpace)
			}
			for _, r := range token {
				width, err = f.measure(line + string(r))
				if err != nil {
					return nil, err
				}
				if width.Ceil() > maxWidth && line != "" {
					lines = append(lines, strings.TrimSpace(line))
					line = ""
				}
				line += string(r)
			}
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines, nil
}

func paintText(mask *image.Alpha, f *titleFaces, text string, x, baseline int) error {
	pos := fixed.P(x, baseline)
	var prev rune
	var last font.Face
	for _, r := range text {
		face, err := f.face(r)
		if err != nil {
			return err
		}
		if last == face {
			pos.X += face.Kern(prev, r)
		}
		d := font.Drawer{Dst: mask, Src: image.White, Face: face, Dot: pos}
		d.DrawString(string(r))
		pos = d.Dot
		prev, last = r, face
	}
	return nil
}

func fill(dst draw.Image, rect image.Rectangle, c color.NRGBA) {
	draw.Draw(dst, rect, image.NewUniform(c), image.Point{}, draw.Over)
}

func maskAt(dst draw.Image, mask *image.Alpha, at image.Point, c color.NRGBA) {
	draw.DrawMask(dst, mask.Bounds().Add(at), image.NewUniform(c), image.Point{}, mask, image.Point{}, draw.Over)
}

func stroked(dst draw.Image, mask *image.Alpha, at image.Point, radius int, c color.NRGBA) {
	// A bounded set of translated glyph masks keeps high-resolution rendering
	// inexpensive while providing an antialiased outline.
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			if x*x+y*y <= radius*radius {
				maskAt(dst, mask, at.Add(image.Pt(x, y)), c)
			}
		}
	}
}

// TitlePNG is a convenience wrapper using assets/fonts near the working directory
// or executable. Use Tools.TitlePNG to supply an explicit FontDir.
func TitlePNG(draft domain.Draft, width, height int) ([]byte, error) {
	return New(Config{}).TitlePNG(draft, width, height)
}

// TitlePNG draws the hook when present, otherwise the title. All nine presets
// produce deterministic transparent PNGs; previews and exports share these pixels.
// Presets are native Go designs, not pixel-identical Python template revisions.
func (t *Tools) TitlePNG(draft domain.Draft, width, height int) (data []byte, err error) {
	if draft.TitleEnabled != nil && !*draft.TitleEnabled {
		if width < 1 || height < 1 || width > 4096 || height > 4096 {
			return nil, errors.New("media: invalid title dimensions")
		}
		var b bytes.Buffer
		err = png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, width, height)))
		return b.Bytes(), err
	}
	if t.cfgErr != nil {
		return nil, t.cfgErr
	}
	if width < 16 || height < 16 || width > 8192 || height > 8192 || int64(width)*int64(height) > 16777216 {
		return nil, errors.New("title canvas must be at least 16x16 and at most 16 megapixels")
	}
	if !slices.Contains(domain.Styles, draft.TitleStyle) {
		return nil, errors.New("unknown title style")
	}
	if !finite(draft.TitleScale) || draft.TitleScale < .75 || draft.TitleScale > 1.2 ||
		!finite(draft.TitleY) || draft.TitleY < .06 || draft.TitleY > .70 {
		return nil, errors.New("invalid title scale or position")
	}
	if draft.TitleTemplateVersion < 0 || draft.TitleTemplateVersion > 6 {
		return nil, errors.New("unsupported title template version")
	}
	if !utf8.ValidString(draft.Title) || !utf8.ValidString(draft.Hook) ||
		len([]rune(draft.Title)) > 200 || len([]rune(draft.Hook)) > 120 {
		return nil, errors.New("invalid title or hook text")
	}
	text := draft.Hook
	if strings.TrimSpace(text) == "" {
		text = draft.Title
	}
	text = normalizeText(text)
	if text == "" {
		return nil, errors.New("title text must not be empty")
	}
	theme := themeFor(draft.TitleStyle)
	if draft.TitleAccent != nil {
		value := *draft.TitleAccent
		if len(value) != 7 || value[0] != '#' {
			return nil, errors.New("invalid title accent; expected #RRGGBB")
		}
		b, e := hex.DecodeString(value[1:])
		if e != nil {
			return nil, fmt.Errorf("title accent: %w", e)
		}
		theme.accent = color.NRGBA{b[0], b[1], b[2], 255}
		if draft.TitleStyle == "impact" || draft.TitleStyle == "comic" {
			theme.text = theme.accent
		}
	}
	size := max(8, math.Min(float64(width)*.10, float64(height)*.105)*draft.TitleScale)
	pad := max(3, int(size*.32))
	maxWidth := width*84/100 - 2*pad
	var faces *titleFaces
	var lines []string
	var lineHeight, ascent int
	for attempt := 0; attempt < 24; attempt++ {
		faces, err = t.titleFaces(theme.font, size)
		if err != nil {
			return nil, err
		}
		lines, err = wrapTitle(faces, text, maxWidth)
		if err != nil {
			return nil, errors.Join(err, faces.close())
		}
		ascent = max(faces.latin.Metrics().Ascent.Ceil(), faces.cjk.Metrics().Ascent.Ceil())
		descent := max(faces.latin.Metrics().Descent.Ceil(), faces.cjk.Metrics().Descent.Ceil())
		lineHeight = ascent + descent + max(1, int(size*.12))
		if len(lines) <= 4 && len(lines)*lineHeight+2*pad <= height*60/100 {
			break
		}
		if err = faces.close(); err != nil {
			return nil, err
		}
		faces = nil
		size *= .9
		if size < 6 {
			break
		}
	}
	if faces == nil {
		return nil, errors.New("title does not fit canvas; shorten text or increase dimensions")
	}
	defer func() { err = errors.Join(err, faces.close()) }()
	mask := image.NewAlpha(image.Rect(0, 0, width*84/100, len(lines)*lineHeight+2*pad))
	for i, line := range lines {
		advance, e := faces.measure(line)
		if e != nil {
			return nil, e
		}
		x := (mask.Rect.Dx() - advance.Ceil()) / 2
		if draft.TitleStyle == "editorial" {
			x = pad
		}
		if err = paintText(mask, faces, line, x, pad+ascent+i*lineHeight); err != nil {
			return nil, err
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	top := max(height*3/100, min(int(float64(height)*draft.TitleY), height-height*5/100-mask.Rect.Dy()))
	at := image.Pt((width-mask.Rect.Dx())/2, top)
	panel := mask.Rect.Add(at)
	if theme.panel.A > 0 {
		fill(img, panel, theme.panel)
	}
	unit := max(1, int(size*.055))
	switch draft.TitleStyle {
	case "plain":
		fill(img, image.Rect(panel.Min.X+pad, panel.Max.Y-unit, panel.Max.X-pad, panel.Max.Y), theme.accent)
	case "card":
		fill(img, image.Rect(panel.Min.X, panel.Min.Y, panel.Min.X+unit*2, panel.Max.Y), theme.accent)
	case "comic":
		fill(img, image.Rect(panel.Min.X+pad, panel.Max.Y-unit*3, panel.Max.X-pad, panel.Max.Y), theme.accent)
	case "neon":
		stroked(img, mask, at, unit*2, color.NRGBA{theme.accent.R, theme.accent.G, theme.accent.B, 60})
		fill(img, image.Rect(panel.Min.X, panel.Max.Y-unit, panel.Max.X, panel.Max.Y), theme.accent)
	case "arena":
		fill(img, image.Rect(panel.Min.X, panel.Min.Y, panel.Min.X+unit*3, panel.Max.Y), theme.accent)
		fill(img, image.Rect(panel.Max.X-unit*3, panel.Min.Y, panel.Max.X, panel.Max.Y), theme.accent)
	case "editorial":
		fill(img, image.Rect(panel.Min.X+pad, panel.Min.Y+unit, panel.Max.X-pad, panel.Min.Y+unit*2), theme.accent)
		fill(img, image.Rect(panel.Min.X+pad, panel.Max.Y-unit*2, panel.Max.X-pad, panel.Max.Y-unit), white)
	case "pixel":
		for x := panel.Min.X; x < panel.Max.X; x += unit * 4 {
			fill(img, image.Rect(x, panel.Max.Y-unit*2, min(x+unit*2, panel.Max.X), panel.Max.Y), theme.accent)
		}
	case "frosted":
		fill(img, image.Rect(panel.Min.X, panel.Min.Y, panel.Max.X, panel.Min.Y+unit), color.NRGBA{255, 255, 255, 210})
		fill(img, image.Rect(panel.Min.X+pad, panel.Max.Y-unit*2, panel.Max.X-pad, panel.Max.Y-unit), theme.accent)
	}
	if theme.shadow > 0 {
		maskAt(img, mask, at.Add(image.Pt(unit*theme.shadow, unit*theme.shadow)), ink)
	}
	if theme.outline > 0 {
		stroked(img, mask, at, min(6, unit*theme.outline), ink)
	}
	maskAt(img, mask, at, theme.text)
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("title PNG: %w", err)
	}
	return out.Bytes(), nil
}
