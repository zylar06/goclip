#!/bin/sh
set -eu
# Build estimate 10–25m, CI caps the whole job at 45m. Inspect unchanged stages.
mkdir -p artifacts
node --test scripts/deployment.test.mjs
docker compose up -d --build --wait --wait-timeout 180
docker compose exec -T worker ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i testsrc2=size=320x180:rate=30 \
  -f lavfi -i anullsrc=r=48000:cl=stereo \
  -t 5 -c:v libx264 -pix_fmt yuv420p -c:a aac /tmp/smoke.mp4
docker compose cp worker:/tmp/smoke.mp4 artifacts/container-fixture.mp4
node scripts/container-smoke.mjs artifacts/container-fixture.mp4
docker compose restart
docker compose up -d --wait --wait-timeout 120
node -e 'fetch("http://127.0.0.1:8080/api/v1/projects").then(r=>r.json()).then(p=>{if(!p.some(x=>x.name==="容器验收"))throw Error("Persistence lost");console.log("PASS persistence")}).catch(e=>{console.error(e);process.exit(1)})'
