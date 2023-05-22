#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

### input (calculated by gardenlet using github.com/google/go-containerregistry)
# URL to the layer blob (sha256 reference) in the registry that contains the node-agent binary (final layer)
blob_url="$1"
blob_digest="${blob_url##*sha256:}"
# path to the node-agent binary in the layer filesystem (determined by entrypoint in image metadata)
blob_binary_path="$2"
target_directory="${3:-/opt/bin}"

### prepare directory
tmp_dir="$(mktemp -d)"
blob="$tmp_dir/gardener-node-agent.tar.gz"
trap 'rm -rf "$tmp_dir"' EXIT

### download + verify
echo "> Downloading blob from $blob_url"
curl -Lv "$blob_url" -o "$blob"

echo "> Verifying checksum of downloaded blob"
echo "$blob_digest $blob" | sha256sum --check

### extract + move
echo "> Extracting gardener-node-agent binary"
tar -xzvf "$blob" -C "$tmp_dir" "$blob_binary_path"

echo "> Ensuring target directory $target_directory"
mkdir -p "$target_directory"

echo "> Moving gardener-node-agent binary to target directory"
mv -f "$tmp_dir$blob_binary_path" "$target_directory/gardener-node-agent"

### bootstrap
echo "> Bootstrapping gardener-node-agent"
"$target_directory/gardener-node-agent" bootstrap
# - use the bootstrap token to download the shoot access token
# - download and execute the cloud-config-secret from the shoot API server
# - ensure a systemd unit which runs 'gardener-node-agent run'
# - remove systemd unit gardener-node-init
# - exit and let systemd unit take over

# 'gardener-node-agent run' takes over responsibilities of cloud-config-downloader and executor:
# - watch the cloud-config secret
# - download and execute the cloud-config secret if it was updated
# - annotate the node with the script's checksum
# - extract the kubelet binary
# - monitor kubelet and containerd
# - cloud-config secret additionally contains gardener-node-agent configuration file (usual component config API)
# - watch its configuration file and download a new version of itself if the specified version changes
