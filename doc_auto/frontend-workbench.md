# GoClip frontend workbench

Updated: 2026-09-30T14:28:00+08:00

## Baseline and scope

The local baseline is `67e167c` on `feat/workflow-parity`. The investigation
started before that commit, when the workflow-parity changes were uncommitted;
no reset, pull, stash, source replacement or production-data migration was used.
Frontend version still follows root `VERSION` (`0.1.0`), not a separate UI release.

The redesign changes presentation and browser interactions only. Existing API
contracts, import gating, billing/image consent, revision fencing, export history,
local draft recovery and backend persistence remain authoritative. No packages,
remote fonts, analytics, model calls or image-generation dependencies were added.

## Design decision

This is an editing workbench, not a product landing page. Lead with real projects
and clips. Use the video itself for visual identity rather than hero copy,
decorative statistics, gradient backgrounds or game-like title thumbnails.

- Light surfaces: `#F6F7F9` canvas, `#FFFFFF` panels, `#ECEFF3` recessed areas.
- Text: `#202631` primary, `#505C6B` secondary, `#606C7A` supporting text.
- Action: `#245BC4`, with semantic red/green/amber reserved for task outcomes.
- Native system sans typography; tabular numeric values for time/duration.
- Compact 4/6/8px control/panel radii. No resting card shadows or hover lift.
- Slate dark theme uses the same components and explicit/native OS theme tokens.
- Keep visible focus, reduced motion, readable labels and mobile stacking.

The first screenshot review found three remaining sources of clutter: raw import
logs expanded every failed project row, completed tasks took the first project
section, and title presets dominated the editor despite being optional.
All three were revised rather than accepted as a color-only redesign.

## Information architecture

- **Project library:** one heading and import action; newest-first project rows,
  project-name search persisted in `?q=`, actual status, duration and updated date.
  Missing dimensions show an inspection state instead of fabricated `0 × 0`.
  Failed rows link to their full project error instead of printing backend logs.
- **Import:** explicit dialog; upload/link mode and exact adapter fields retained.
  Cancel/Escape restore trigger focus and clear file selections. Busy upload
  cannot be dismissed or resubmitted; errors remain visible. Optional instructions
  remain in a disclosure; focus trapping skips collapsed contents.
- **Project:** results before source-management details. Only unfinished/failed
  tasks occupy the activity area. Successful tasks, past workflows, old exports,
  source ranges and technical heartbeats are accessible through disclosures.
  No repeated readiness/draft/export summary strip.
- **Plan:** existing clips collapse the plan by default without unmounting its
  recovery state. A confirmed plan has a compact receipt and explicit new-round
  action; it does not display a second apparent permission form. Unknown
  confirmation outcomes retain the saved-revision reconfirm path, not this receipt.
  New rounds consume fresh consent and keep the save-before-confirm contract.
- **Review:** source preview and plan side by side on desktop, stacked on mobile.
  Compatibility conversion remains explicit/local; errors and active conversion
  automatically expand the compatibility controls.
- **Editor:** wider video area, compact inspector, title templates disclosed only
  when requested. Real playback, title options, scene edits and save/export survive.
- **Settings:** text/vision models side by side, cookies below; installation version
  and trusted-LAN/shared-cost boundary appear here instead of every page footer.

## External skills consulted (read-only)

Retrieved public instruction text during the 2026-09-30 investigation:

1. Anthropic `frontend-design`: subject-specific visual choices, remove generated
   interface clichés, plain copy and screenshot-based self-review.
2. Vercel `web-design-guidelines`: native controls, visible focus, accessible labels,
   overflow handling, reduced motion, real empty/error states.
3. Vercel `react-best-practices`: derive filtering during render, preserve stable
   component boundaries, avoid unnecessary dependencies/effects.

Source locations:

```text
https://github.com/anthropics/skills/tree/main/skills/frontend-design
https://github.com/vercel-labs/agent-skills/tree/main/skills/web-design-guidelines
https://github.com/vercel-labs/agent-skills/tree/main/skills/react-best-practices
```

These skills were consulted, not globally installed. No remote scripts or
installers were executed. A preliminary Impeccable path returned 404; it is not
claimed as an applied skill. Raw guidelines/repository research metadata are in
ignored `artifacts/frontend-redesign/`.

## Verification entry points

```text
cd web
npm run typecheck
npm test
npm run build
cd ..
node --test scripts/parity-browser.test.mjs
node scripts/frontend-browser.mjs
node scripts/parity-browser.mjs
```

`frontend-browser.mjs` requires local Chrome/Edge and an existing API at loopback
8080 (or `AUTOCLIP_PREVIEW_ORIGIN`). It serves only the built local frontend,
proxies GET/HEAD only, rejects and records writes, captures desktop/mobile/dark
screenshots, checks overflow and real media readiness, and checks Escape focus.
It has a 120-second work deadline, bounded probes and process cleanup. Cancelled
read streams during navigation/cleanup are counted separately from API errors.
Artifacts stay under a unique ignored `artifacts/frontend-redesign/browser-*`.

`parity-browser.mjs` is the independent full workflow check using an isolated
database/worker and deterministic loopback model replies. Its home interaction
now opens the import dialog before choosing video/SRT. It is not a paid-provider
or production-data test. This redesign does not claim new live-model quality.

Final verification numbers and deployment/preview boundaries are appended to
`frontend.md` and `verification.md` after execution. Screenshots do not constitute
subjective user approval that an interface has no AI-generated appearance.
