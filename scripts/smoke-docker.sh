#!/bin/sh
set -eu
# CI caps the whole job at 45m. Download speed dominates a cold build; inspect stalled stages.
mkdir -p artifacts
node --test scripts/apt-setup.test.mjs scripts/deployment.test.mjs scripts/smoke-docker.test.mjs
docker compose up -d --build --wait --wait-timeout 180
docker compose exec -T worker ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i testsrc2=size=320x180:rate=30 \
  -f lavfi -i anullsrc=r=48000:cl=stereo \
  -t 5 -c:v libx264 -pix_fmt yuv420p -c:a aac /tmp/smoke.mp4
# /tmp is a live tmpfs mount, not an image-layer file for Docker's archive API.
# Stream inside the running container with no TTY; sh redirects binary bytes.
# set -e stops immediately if cat/exec fails, before uploading a partial fixture.
docker compose exec -T worker cat /tmp/smoke.mp4 > artifacts/container-fixture.mp4
node scripts/container-smoke.mjs artifacts/container-fixture.mp4
docker compose restart
docker compose up -d --wait --wait-timeout 120
node -e 'fetch((process.env.AUTOCLIP_URL||"http://127.0.0.1:8080/api/v1")+"/projects",{signal:AbortSignal.timeout(30000)}).then(r=>{if(!r.ok)throw Error("Persistence check HTTP "+r.status);return r.json()}).then(p=>{if(!p.some(x=>x.name==="容器验收"))throw Error("Persistence lost");console.log("PASS persistence")}).catch(e=>{console.error(e);process.exit(1)})'
