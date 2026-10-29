#!/usr/bin/env bash
# Builds both images and imports them straight into k3s's containerd on each
# node, so you don't need a container registry for a homelab cluster.
#
# Usage: scripts/k3s-load-images.sh user@k3s-server [user@k3s-agent ...]
# Needs: Docker locally, SSH access to each node, sudo on the nodes.
set -euo pipefail

if [ $# -eq 0 ]; then
  echo "Usage: $0 user@node1 [user@node2 ...]"
  exit 1
fi

cd "$(dirname "$0")/.."

for cmd in api worker; do
  echo "Building ev-chargers-$cmd:dev"
  # k3s VMs on Proxmox are almost always amd64; build for that even on an
  # Apple Silicon Mac.
  docker build --platform linux/amd64 --build-arg CMD="$cmd" -t "ev-chargers-$cmd:dev" .
done

docker save ev-chargers-api:dev ev-chargers-worker:dev -o images.tar

for node in "$@"; do
  echo "Importing into $node"
  scp images.tar "$node:/tmp/ev-chargers-images.tar"
  ssh "$node" "sudo k3s ctr images import /tmp/ev-chargers-images.tar && rm /tmp/ev-chargers-images.tar"
done

rm images.tar
echo "Images loaded. If pods are already running: kubectl -n ev-chargers rollout restart deploy"
