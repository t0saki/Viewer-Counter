#!/usr/bin/env bash
# Minimal build script: produces a self-contained output/ directory with the
# binary, a config template, and a bootstrap entry script.
#
#   ./build.sh                 # build for the host platform
#   CROSS_COMPILE=TRUE ./build.sh   # static build for linux/amd64 (deploy)
set -euo pipefail

APP_NAME="viewer-counter"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

rm -rf output
mkdir -p output/bin output/conf

LDFLAGS="-s -w"
if [ "${CROSS_COMPILE:-}" = "TRUE" ]; then
    echo "build: cross compile -> linux/amd64 (static)"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -ldflags "$LDFLAGS" -o "output/bin/$APP_NAME" ./cmd/server
else
    echo "build: native"
    CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "output/bin/$APP_NAME" ./cmd/server
fi
echo "succeeded in building $APP_NAME"

# Ship the config template; override real values (db.dsn, privacy.salt,
# auth.admin_tokens, ...) via VC_* env vars at runtime.
cp config.example.yaml output/conf/config.yaml

# Bootstrap entry: started by the deploy platform / run manually.
cat > output/bootstrap.sh <<'EOF'
#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
exec ./bin/viewer-counter -config conf/config.yaml
EOF
chmod +x output/bootstrap.sh

echo "output/ ready:"
echo "  output/bin/$APP_NAME"
echo "  output/conf/config.yaml"
echo "  output/bootstrap.sh"
