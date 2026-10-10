#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 ChattoCorp GmbH
# SPDX-License-Identifier: AGPL-3.0-or-later

# Run the bundled frontend and server through a private forwarded HTTP port.
# External OIDC and LiveKit need connectivity that Codespaces does not forward.
set -euo pipefail

project_dir=$(cd "$(dirname "$0")/.." && pwd -P)
port=${CHATTO_DEV_PORT_BASE:-4000}
review_root="$project_dir/.context/dev/review"
data_root=${CHATTO_DEV_DATA_ROOT:-$review_root/data}
mkdir -p "$review_root" "$data_root/bootstrap" "$data_root/nats" "$data_root/search"

# Keep the regular development settings and bootstrap users. Remove external
# providers and bots that need services outside the review server.
config_path="$review_root/chatto.toml"
awk '
  /^\[\[(auth\.providers|bootstrap\.bots)\]\]$/ { skip = 1; next }
  /^\[/ { skip = 0 }
  !skip { print }
' "$project_dir/cli/chatto.toml" > "$config_path"

if [[ -n ${CODESPACE_NAME:-} ]]; then
  forwarding_domain=${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-app.github.dev}
  public_url="https://$CODESPACE_NAME-$port.$forwarding_domain"
else
  public_url="http://localhost:$port"
fi

export CHATTO_WEBSERVER_URL="$public_url"
export CHATTO_WEBSERVER_PORT="$port"
export CHATTO_NATS_EMBEDDED_PORT=$((port + 4))
export CHATTO_NATS_EMBEDDED_DATA_DIR="$data_root/nats"
export CHATTO_SEARCH_PROVIDER_DIRECTORY="$data_root/search"
export CHATTO_OPERATOR_API_SOCKET_PATH="$data_root/operator/operator.sock"
export CHATTO_SMTP_ENABLED=false
export CHATTO_LIVEKIT_ENABLED=false
export CHATTO_METRICS_ENABLED=false
export CHATTO_EXPORTER_ENABLED=false

echo "Chatto: $public_url"
cd "$project_dir/cli"
exec bin/chatto run --config "$config_path"
