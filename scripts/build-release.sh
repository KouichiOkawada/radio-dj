#!/usr/bin/env bash
# Build stripped, cross-compiled binaries for GitHub Releases.
# Output naming MUST match install.sh's expected pattern: radio-dj-<os>-<arch>
#   install.sh downloads: .../releases/<tag>/download/radio-dj-$OS-$ARCH
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
OUT="dist"
rm -rf "$OUT"; mkdir -p "$OUT"

TARGETS=(
  "darwin  arm64"   # Apple Silicon (M1+)
  "darwin  amd64"   # Intel Mac
  "linux   arm64"   # Raspberry Pi 4/5 (64-bit)
  "linux   amd64"   # x86_64 servers
)

for t in "${TARGETS[@]}"; do
  set -- $t
  GOOS="$1" GOARCH="$2" CGO_ENABLED=0 \
    go build -trimpath -ldflags="$LDFLAGS" -o "$OUT/radio-dj-$1-$2" .
  SZ=$(ls -lh "$OUT/radio-dj-$1-$2" | awk '{print $5}')
  printf '✓ %-18s %s\n' "radio-dj-$1-$2" "$SZ"
done

echo ""
echo "Built $OUT/ for release: $VERSION"
