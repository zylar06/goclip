# Licensed media fonts

Updated: 2026-09-29

All files listed in `manifest.json`, including that manifest and the four OFL
licenses, were copied unchanged from `../autoclip/backend/assets/fonts`.
The upstream manifest pins Google Fonts commit
`e44c4b011a820c2cbe2fd2cfa8052037d7edb571` and each original file's SHA-256.
The supplied Windows checkout has CRLF license line endings; the upstream
manifest mixes LF and CRLF hashes. License checksum verification accepts only
those line-ending differences. Font binary hashes are checked byte for byte.
The copied license files themselves are unchanged.

- Anton: `Anton-OFL.txt`
- Barlow Condensed: `BarlowCondensed-OFL.txt`
- Noto Sans SC: `NotoSansSC-OFL.txt`
- Press Start 2P: `PressStart2P-OFL.txt`

`NotoSansSC.ttf` is the original **variable** TrueType font (`fvar`, `gvar`).
It is retained for provenance but is **not** loaded by the Go renderer.
`NotoSansSC-StaticBold.ttf` is a full, non-subset static instance at weight 700.
It retains all 30,890 Unicode cmap entries and the original OFL license.
Its source/output hashes and conversion settings are in `static-manifest.json`.

The one-time conversion used FontTools 4.60.1, loaded the source with
`recalcTimestamp=False`, instantiated `wght=700` using
`fontTools.varLib.instancer.instantiateVariableFont`, and saved with timestamp
recalculation disabled. No `fvar`, `gvar` or `CFF2` table remains. OS/2 weight is
700; the original family/name metadata is retained. No Python code or FontTools
installation is required to build, deploy or run AutoClip Go.

The renderer uses `golang.org/x/image/font/opentype`. Latin style fonts fall back
to the static Noto font per glyph. Missing glyphs/fonts fail explicitly instead
of substituting a platform-dependent font. FFmpeg/libass receives a private copy
of the static Noto font for subtitles, avoiding reliance on installed CJK fonts.

Update: 2026-09-29 — copied original licensed fonts; added reproducible static
Noto Bold instance and checksum/variable-table regression tests.

Update: 2026-09-29T17:50:00+08:00 — The static font's legacy family name remains
`Noto Sans SC Thin`, although its outlines/OS/2 weight are the fixed Bold (700)
instance. ASS must request this exact legacy family so libass selects the
private font instead of relying on a system CJK fallback. The typographic
family `Noto Sans SC` did not match in the verified Linux/Windows libass builds.
No font file, license or checksum was changed.
