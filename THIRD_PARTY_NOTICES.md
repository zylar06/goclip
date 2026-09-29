# Third-party notices

Updated: 2026-09-29

- React studio components, styles, translation catalogs and prompts are adapted
  from `zhouxiaoka/autoclip`, MIT, Copyright (c) 2024 AutoClip Team.
  The original MIT LICENSE is retained.
- Go modules: license/source information remains in upstream modules; versions
  and integrity records are committed in `go.mod` / `go.sum`.
- Frontend module versions and integrity are committed in `web/package-lock.json`.
- Font licenses are distributed next to their font files in `assets/fonts`.
- whisper.cpp (MIT), Whisper base weights (MIT), Deno (MIT), yt-dlp (Unlicense
  project; standalone bundle includes separately licensed dependencies):
  upstream notices copied into `/usr/share/autoclip/licenses` during build.
- Debian FFmpeg and its libx264-enabled build include GPL components. The
  container is **not exclusively MIT**. Debian package copyrights and notices
  remain in `/usr/share/doc`; exact packages are recorded in
  `/usr/share/autoclip/debian-packages.txt`. Matching source packages are
  available in the signed Debian snapshot listed in `tools.lock.json`.
- If redistributing container binaries, review the licenses of every bundled
  dependency and satisfy corresponding-source/notice obligations. Building
  this project does not grant rights to download or republish third-party videos.
