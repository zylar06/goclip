# AutoClip web

Web-only React port of the upstream Studio editor. Same-origin Go API; no native
runtime, telemetry, remote fonts, publishing, or user-account layer.

Requires Node 22.12+ (tested with 22.22.2).

```sh
npm ci
npm run generate:api
npm run typecheck
npm test
npm run build
```

`generate:api` reads `../api/openapi.json` and writes the **tracked**
`src/generated/api.ts`. `typecheck` includes `check:api` and fails on stale generated
types. The Docker frontend build stage must copy both `../api/openapi.json` and
`../VERSION`; application version is read directly from `../VERSION` by Vite.

`npm run dev` binds to `127.0.0.1` and proxies `/api` to `http://127.0.0.1:8080`.
Set `AUTOCLIP_API_ORIGIN` in the development environment for a different backend.
Production uses only relative `/api/v1` URLs and the backend serves `web/dist`.
Hash navigation works without static-host rewrite rules.

`npm run preview` also binds to `127.0.0.1`; neither development command exposes
the server on all interfaces by default.

See `../doc_auto/frontend.md` for provenance, backend interfaces, reliability
rules, behavior tests, limitations, and the complete owned-file inventory.
