#!/bin/sh
set -eu
# Configure a container's APT; the optional absolute directory permits offline tests.
# Package installation is deliberately left to Docker's cache-mounted RUN steps.
fail() { printf '%s\n' "$1" >&2; exit 1; }
[ "$#" -le 1 ] || fail "expected at most one absolute APT configuration directory"
apt_dir=${1:-/etc/apt}
case "$apt_dir" in /*) ;; *) fail "APT configuration directory must be absolute" ;; esac
mode=${APT_SOURCE_MODE:-mirror}
snapshot=${DEBIAN_SNAPSHOT:-20260901T000000Z}
mirror=${DEBIAN_MIRROR:-https://deb.debian.org/debian}
security_mirror=${DEBIAN_SECURITY_MIRROR:-https://deb.debian.org/debian-security}

validate_mirror() {
    case "$1" in http://*|https://*) ;; *) fail "invalid mirror URL: HTTP(S) is required" ;; esac
    case "$1" in
        *[!a-zA-Z0-9:/._~-]*) fail "invalid mirror URL: credentials, queries and whitespace are not allowed" ;;
    esac
    authority=${1#*://}
    authority=${authority%%/*}
    case "$authority" in ''|:*) fail "invalid mirror URL: a hostname is required" ;; esac
}

case "$mode" in
    mirror)
        validate_mirror "$mirror"
        validate_mirror "$security_mirror"
        mirror=${mirror%/}
        security_mirror=${security_mirror%/}
        source_options=
        ;;
    snapshot)
        printf '%s\n' "$snapshot" | grep -Eq '^[0-9]{8}T[0-9]{6}Z$' \
            || fail "invalid Debian snapshot timestamp"
        mirror="http://snapshot.debian.org/archive/debian/$snapshot"
        security_mirror="http://snapshot.debian.org/archive/debian-security/$snapshot"
        # Only archived metadata bypasses expiry; signatures and package hashes stay required.
        source_options='[check-valid-until=no] '
        ;;
    *) fail "unsupported APT_SOURCE_MODE: use mirror or snapshot" ;;
esac

# Validate all inputs before replacing the base image's sources or cleanup hooks.
mkdir -p "$apt_dir/sources.list.d" "$apt_dir/apt.conf.d"
rm -f "$apt_dir/sources.list.d/debian.sources" "$apt_dir/apt.conf.d/docker-clean"
cat > "$apt_dir/sources.list" <<EOF
deb ${source_options}${mirror} bookworm main
deb ${source_options}${mirror} bookworm-updates main
deb ${source_options}${security_mirror} bookworm-security main
EOF
cat > "$apt_dir/apt.conf.d/80autoclip" <<'EOF'
APT::Keep-Downloaded-Packages "true";
Binary::apt::APT::Keep-Downloaded-Packages "true";
Acquire::Retries "2";
Acquire::http::Timeout "30";
Acquire::https::Timeout "30";
APT::Update::Error-Mode "any";
EOF
printf 'AutoClip APT source mode: %s\n' "$mode"
