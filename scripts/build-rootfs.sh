#!/usr/bin/env bash
# build-rootfs.sh — bake a Firecracker microVM rootfs (ext4) plus kernel for
# selfcloud. Designed to be run by an operator on the host; the resulting
# files land under <DataDir>/firecracker/templates/ and are consumed by
# internal/runtime/firecracker/jailer.go at invoke time.
#
# Requirements on the host:
#   - docker (to export a containerised rootfs reproducibly)
#   - mkfs.ext4, dd, mount, umount (mkfs.ext4 from e2fsprogs)
#   - curl (only for kernel download)
#   - root or a sudo-capable user (mount + losetup)
#
# Usage:
#   scripts/build-rootfs.sh \
#       --name node-22 \
#       --base node:22-alpine \
#       --agent ./bin/fc-agent \
#       --out ./data/firecracker/templates
#
# This produces:
#   <out>/rootfs/<name>.ext4            ext4 disk image with fc-agent as PID 1
#   <out>/kernel/vmlinux                Firecracker-compatible kernel (downloaded once)

set -euo pipefail

NAME=""
BASE=""
AGENT=""
OUT=""
SIZE_MB=${SIZE_MB:-256}
KERNEL_URL=${KERNEL_URL:-"https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/x86_64/vmlinux-6.1.102"}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name)   NAME="$2";  shift 2 ;;
    --base)   BASE="$2";  shift 2 ;;
    --agent)  AGENT="$2"; shift 2 ;;
    --out)    OUT="$2";   shift 2 ;;
    --size)   SIZE_MB="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[[ -z "$NAME"  ]] && { echo "missing --name"  >&2; exit 2; }
[[ -z "$BASE"  ]] && { echo "missing --base"  >&2; exit 2; }
[[ -z "$AGENT" ]] && { echo "missing --agent" >&2; exit 2; }
[[ -z "$OUT"   ]] && { echo "missing --out"   >&2; exit 2; }

if [[ ! -x "$AGENT" ]]; then
  echo "fc-agent not found or not executable at $AGENT" >&2
  echo "build it with: make firecracker-agent" >&2
  exit 1
fi

mkdir -p "$OUT/rootfs" "$OUT/kernel"

KERNEL="$OUT/kernel/vmlinux"
if [[ ! -f "$KERNEL" ]]; then
  echo "==> downloading kernel from $KERNEL_URL"
  curl -fsSL --retry 3 -o "$KERNEL" "$KERNEL_URL"
fi

WORK="$(mktemp -d)"
trap 'sudo umount "$WORK/mnt" 2>/dev/null || true; rm -rf "$WORK"' EXIT

ROOTFS="$OUT/rootfs/${NAME}.ext4"

echo "==> exporting rootfs from $BASE"
docker create --name "fc-export-$$" "$BASE" /bin/true >/dev/null
docker export "fc-export-$$" -o "$WORK/rootfs.tar"
docker rm "fc-export-$$" >/dev/null

echo "==> building $ROOTFS (${SIZE_MB} MiB)"
dd if=/dev/zero of="$ROOTFS" bs=1M count="$SIZE_MB" status=none
mkfs.ext4 -q -F "$ROOTFS"
mkdir -p "$WORK/mnt"
sudo mount -o loop "$ROOTFS" "$WORK/mnt"
sudo tar -xf "$WORK/rootfs.tar" -C "$WORK/mnt"

echo "==> baking fc-agent as /sbin/init"
sudo install -m 0755 "$AGENT" "$WORK/mnt/sbin/init"
# Keep a copy as /usr/local/bin/fc-agent for debugging.
sudo install -m 0755 "$AGENT" "$WORK/mnt/usr/local/bin/fc-agent"
# Make sure /code mount point exists for the code drive.
sudo mkdir -p "$WORK/mnt/code" "$WORK/mnt/srv"

# Minimal /etc/hostname + /etc/hosts so DNS-less guests don't error out.
echo "selfcloud-fn" | sudo tee "$WORK/mnt/etc/hostname" >/dev/null
sudo tee "$WORK/mnt/etc/hosts" >/dev/null <<EOF
127.0.0.1   localhost selfcloud-fn
::1         localhost
EOF

sudo umount "$WORK/mnt"
echo "==> wrote $ROOTFS"
echo "==> wrote $KERNEL"
echo
echo "Done. Restart selfcloud and the new template will appear under"
echo "GET /api/v1/runtime/firecracker/templates."
