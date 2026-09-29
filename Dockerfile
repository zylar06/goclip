# syntax=docker/dockerfile:1.7
# Linux amd64 only. Runtime native binaries/model are verified against fixed hashes.
FROM node:22.23.3-bookworm-slim@sha256:43ac6c60b8f89723f746e8a92ce91abd5017e627ce1ddfe4238355d3a30b772c AS frontend
WORKDIR /src
COPY VERSION ./VERSION
COPY api/ ./api/
COPY web/package*.json ./web/
WORKDIR /src/web
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run typecheck && npm test && npm run build

FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go test -timeout 180s ./... \
    && CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$(cat VERSION)" -o /out/autoclip ./cmd/autoclip

FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 AS native
ARG TARGETARCH
COPY scripts/apt-snapshot.sh /tmp/apt-snapshot.sh
RUN test "${TARGETARCH:-amd64}" = amd64 && sh /tmp/apt-snapshot.sh \
    && apt-get install -y --no-install-recommends ca-certificates curl unzip cmake build-essential \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fL --retry 3 --connect-timeout 30 --max-time 600 \
      https://codeload.github.com/ggml-org/whisper.cpp/tar.gz/refs/tags/v1.9.4 -o whisper.tar.gz \
    && echo "57e280cee375ab02425b806ad5146b99f6eb9357e3c2b31357c8a6af2e2e44ae  whisper.tar.gz" | sha256sum -c - \
    && tar xzf whisper.tar.gz \
    && cmake -S whisper.cpp-1.9.4 -B whisper-build \
      -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF \
      -DGGML_CUDA=OFF -DGGML_METAL=OFF -DGGML_OPENMP=OFF \
      -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=ON \
    && cmake --build whisper-build --config Release --target whisper-cli -j2 \
    && mkdir -p /out/bin /out/models /out/licenses \
    && cp whisper-build/bin/whisper-cli /out/bin/ \
    && cp whisper.cpp-1.9.4/LICENSE /out/licenses/whisper.cpp-MIT.txt
RUN curl -fL --retry 3 --connect-timeout 30 --max-time 1200 \
      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin -o /out/models/ggml-base.bin \
    && echo "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe  /out/models/ggml-base.bin" | sha256sum -c -
RUN curl -fL --retry 3 --connect-timeout 30 --max-time 600 \
      https://github.com/yt-dlp/yt-dlp/releases/download/2026.08.19/yt-dlp_linux -o /out/bin/yt-dlp \
    && echo "58162f9bfdc27458ea47bfcb311cf47028f17d8154a8bf7d689861d46399230a  /out/bin/yt-dlp" | sha256sum -c - \
    && curl -fL --retry 3 --connect-timeout 30 --max-time 600 \
      https://github.com/denoland/deno/releases/download/v2.9.7/deno-x86_64-unknown-linux-gnu.zip -o deno.zip \
    && echo "c6527f24f4b16031d3ae4fa9f658d5f11534c8d84ce7dc8502420280919c3490  deno.zip" | sha256sum -c - \
    && unzip deno.zip -d /out/bin \
    && chmod 755 /out/bin/* \
    && curl -fL --retry 3 --max-time 120 https://raw.githubusercontent.com/yt-dlp/yt-dlp/2026.08.19/LICENSE -o /out/licenses/yt-dlp.txt \
    && curl -fL --retry 3 --max-time 120 https://raw.githubusercontent.com/denoland/deno/v2.9.7/LICENSE.md -o /out/licenses/deno-MIT.txt \
    && curl -fL --retry 3 --max-time 120 https://raw.githubusercontent.com/openai/whisper/v20250625/LICENSE -o /out/licenses/whisper-model-MIT.txt

FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 AS runtime
COPY scripts/apt-snapshot.sh /tmp/apt-snapshot.sh
RUN sh /tmp/apt-snapshot.sh \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg fontconfig libstdc++6 libgomp1 \
    && mkdir -p /usr/share/autoclip /app /data /models \
    && dpkg-query -W > /usr/share/autoclip/debian-packages.txt \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 autoclip \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /tmp autoclip
COPY --from=native /out/bin/ /usr/local/bin/
COPY --from=native /out/models/ /models/
COPY --from=native /out/licenses/ /usr/share/autoclip/licenses/
COPY --from=backend /out/autoclip /usr/local/bin/autoclip
COPY --from=frontend /src/web/dist/ /app/web/
COPY assets/fonts/ /app/assets/fonts/
COPY LICENSE THIRD_PARTY_NOTICES.md tools.lock.json /usr/share/autoclip/
RUN fc-cache -f /app/assets/fonts && chown -R 10001:10001 /data /models
WORKDIR /app
ENV AUTOCLIP_ADDR=0.0.0.0:8080 AUTOCLIP_DATA_DIR=/data AUTOCLIP_WEB_DIR=/app/web \
    WHISPER_MODEL=/models/ggml-base.bin FONT_DIR=/app/assets/fonts \
    HOME=/tmp XDG_CACHE_HOME=/tmp/cache
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=4 CMD ["autoclip","healthcheck"]
ENTRYPOINT ["autoclip"]
CMD ["web"]
