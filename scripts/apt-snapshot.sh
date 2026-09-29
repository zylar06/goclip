#!/bin/sh
set -eu
# Signed Debian snapshot fixes native package versions for repeatable builds.
# Archived Release metadata is past Valid-Until; signatures/hash checks remain on.
rm -f /etc/apt/sources.list.d/debian.sources
cat > /etc/apt/sources.list <<'EOF'
deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260901T000000Z bookworm main
deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20260901T000000Z bookworm-updates main
deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20260901T000000Z bookworm-security main
EOF
apt-get -o Acquire::Retries=3 -o Acquire::http::Timeout=60 update
