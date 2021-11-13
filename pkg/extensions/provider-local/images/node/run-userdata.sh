#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

userdata_path="${1:-/etc/gardener-worker/userdata}"
if [[ -f "$userdata_path" ]]; then
  echo "Executing userdata at $userdata_path"
  "$userdata_path"
fi
