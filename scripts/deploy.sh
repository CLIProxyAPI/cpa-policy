#!/usr/bin/env bash
# Build the Linux/amd64 plugin, install it on the CPA server from .env,
# restart CPA and check that the new library loaded.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ -f .env ]] || { echo 'Create .env from .env.example before deploying.' >&2; exit 1; }
chmod 600 .env
. ./.env
: "${CPA_SSH_HOST:?Set CPA_SSH_HOST in .env}"
: "${CPA_REMOTE_PLUGIN_DIR:?Set CPA_REMOTE_PLUGIN_DIR in .env}"
: "${CPA_REMOTE_COMPOSE_FILE:?Set CPA_REMOTE_COMPOSE_FILE in .env}"
: "${CPA_BASE_URL:?Set CPA_BASE_URL in .env}"
: "${CPA_MANAGEMENT_PASSWORD:?Set CPA_MANAGEMENT_PASSWORD in .env}"

id=cpa-codex-controls
service=cli-proxy-api
version=$(sed -n 's/^[[:space:]]*Version[[:space:]]*= "\(.*\)"/\1/p' internal/plugin/types.go)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || { echo 'Cannot determine the plugin version.' >&2; exit 1; }
target=$id-v$version.so
# Backups go beside the plugins/ directory, where the loader does not scan them.
[[ /$CPA_REMOTE_PLUGIN_DIR/ == */plugins/* ]] || { echo 'CPA_REMOTE_PLUGIN_DIR must be inside a plugins/ directory.' >&2; exit 1; }
plugins_parent=/$CPA_REMOTE_PLUGIN_DIR/
plugins_parent=${plugins_parent%/plugins/*}
backups=${plugins_parent#/}${plugins_parent:+/}plugin-backups
stamp=$(date -u +%Y%m%d-%H%M%S)

echo "==> Building $target"
mkdir -p dist
ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$PWD/dist/.zig-cache}" \
  GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC='zig cc -target x86_64-linux-gnu.2.17' \
  go build -trimpath -ldflags='-s -w' -tags cshared -buildmode=c-shared -o "dist/$id.so" "./cmd/$id"
sum=$(shasum -a 256 "dist/$id.so" | cut -d' ' -f1)
echo "sha256 $sum"

echo "==> Installing on the CPA server"
scp -q "dist/$id.so" "$CPA_SSH_HOST:$CPA_REMOTE_PLUGIN_DIR/.$id.upload"
ssh "$CPA_SSH_HOST" bash -s -- "$CPA_REMOTE_PLUGIN_DIR" "$backups" "$id" "$target" "$sum" "$stamp" "$CPA_REMOTE_COMPOSE_FILE" "$service" <<'EOF'
set -euo pipefail
dir=$1 backups=$2 id=$3 target=$4 sum=$5 stamp=$6 compose=$7 service=$8
echo "$sum  $dir/.$id.upload" | sha256sum -c --quiet -
mkdir -p "$backups"
for file in "$dir/$id"-v*.so; do
  [ -e "$file" ] || continue
  mv "$file" "$backups/${file##*/}.$stamp"
  echo "backup plugin-backups/${file##*/}.$stamp"
done
mv "$dir/.$id.upload" "$dir/$target"
docker compose -f "$compose" restart "$service"
docker compose -f "$compose" ps "$service"
EOF

echo "==> Checking plugin status"
for _ in {1..20}; do
  if printf 'Authorization: Bearer %s\n' "$CPA_MANAGEMENT_PASSWORD" |
    curl -fsS --connect-timeout 10 --max-time 30 --header @- "${CPA_BASE_URL%/}/v0/management/plugins" 2>/dev/null |
    jq -e --arg id "$id" --arg target "$target" --arg version "$version" \
      '.plugins[] | select(.id == $id and .registered and .effective_enabled and .metadata.version == $version and (.path | endswith("/" + $target))) | {id, version: .metadata.version, registered, effective_enabled}'; then
    exit 0
  fi
  sleep 2
done
echo "$target did not load; check the store.version pin or restore the backup above." >&2
exit 1
