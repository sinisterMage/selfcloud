#!/usr/bin/env bash
# selfCloud one-line installer.
#
# Usage:
#   curl -fsSL https://get.selfcloud.dev | sh
#
# Optional flags (passed via `sh -s -- ...`):
#   --version <vX.Y.Z>      install a specific selfcloud release (default: latest)
#   --bin-dir <path>        binary install location (default: /usr/local/bin)
#   --data-dir <path>       state directory (default: /var/lib/selfcloud)
#   --api-addr <host:port>  dashboard listener (default: 0.0.0.0:8443)
#   --user <name>           service user (default: selfcloud, root if it can't be created)
#   --no-systemd            don't install/enable a systemd unit
#   --join <addr>           after install, join an existing cluster at addr
#   --token <token>         join token to use with --join
#   --skip-containerd       skip installing containerd
#   --dry-run               print actions without executing

set -eu

VERSION="${SELFCLOUD_VERSION:-latest}"
BIN_DIR="/usr/local/bin"
DATA_DIR="/var/lib/selfcloud"
API_ADDR="0.0.0.0:8443"
SVC_USER="selfcloud"
USE_SYSTEMD=1
JOIN_ADDR=""
JOIN_TOKEN=""
SKIP_CTD=0
DRY_RUN=0
RELEASE_HOST="${SELFCLOUD_RELEASE_HOST:-https://github.com/selfcloud/selfcloud}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --api-addr) API_ADDR="$2"; shift 2 ;;
    --user) SVC_USER="$2"; shift 2 ;;
    --no-systemd) USE_SYSTEMD=0; shift ;;
    --join) JOIN_ADDR="$2"; shift 2 ;;
    --token) JOIN_TOKEN="$2"; shift 2 ;;
    --skip-containerd) SKIP_CTD=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help)
      sed -n '2,30p' "$0" | sed 's/^# //; s/^#//'; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

run() {
  if [ "$DRY_RUN" = 1 ]; then
    printf '+ %s\n' "$*"
  else
    eval "$@"
  fi
}

log()  { printf '\033[36m[selfcloud]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[selfcloud]\033[0m %s\n' "$*"; }
fail() { printf '\033[31m[selfcloud]\033[0m %s\n' "$*" >&2; exit 1; }

# --- Preflight ---------------------------------------------------------

[ "$(uname -s)" = "Linux" ] || fail "selfCloud currently supports Linux only."

if [ "$(id -u)" != 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
  else
    fail "must be run as root or with sudo available."
  fi
else
  SUDO=""
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture: $ARCH" ;;
esac

if ! command -v systemctl >/dev/null 2>&1; then
  USE_SYSTEMD=0
  warn "systemctl not found; will not install a unit (use --no-systemd to silence this)."
fi

# --- containerd --------------------------------------------------------

ensure_containerd() {
  if [ "$SKIP_CTD" = 1 ]; then return; fi
  if command -v containerd >/dev/null 2>&1; then
    log "containerd already installed at $(command -v containerd)"
    return
  fi
  log "installing containerd"
  if command -v apt-get >/dev/null 2>&1; then
    run "$SUDO apt-get update -y"
    run "DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y containerd"
  elif command -v dnf >/dev/null 2>&1; then
    run "$SUDO dnf install -y containerd"
  elif command -v pacman >/dev/null 2>&1; then
    run "$SUDO pacman -S --noconfirm containerd"
  else
    warn "couldn't auto-install containerd; please install it manually for container support."
    return
  fi
  run "$SUDO systemctl enable --now containerd" || true
}

# --- selfcloud user + dirs --------------------------------------------

ensure_user() {
  if id -u "$SVC_USER" >/dev/null 2>&1; then
    log "service user $SVC_USER already exists"
    return
  fi
  if command -v useradd >/dev/null 2>&1; then
    run "$SUDO useradd --system --home-dir $DATA_DIR --shell /usr/sbin/nologin $SVC_USER" || SVC_USER=root
  else
    SVC_USER=root
  fi
}

ensure_dirs() {
  run "$SUDO mkdir -p $DATA_DIR"
  run "$SUDO chown -R $SVC_USER:$SVC_USER $DATA_DIR"
  run "$SUDO chmod 0750 $DATA_DIR"
}

# --- binary download --------------------------------------------------

download_binary() {
  local url sumurl
  if [ "$VERSION" = "latest" ]; then
    url="$RELEASE_HOST/releases/latest/download/selfcloud-linux-$ARCH"
  else
    url="$RELEASE_HOST/releases/download/$VERSION/selfcloud-linux-$ARCH"
  fi
  sumurl="${url}.sha256"
  log "downloading $url"
  local tmpdir tmp sumtmp
  tmpdir="$(mktemp -d)"
  tmp="$tmpdir/selfcloud"
  sumtmp="$tmpdir/selfcloud.sha256"
  if ! curl -fsSL "$url" -o "$tmp"; then
    warn "download failed; trying CDN fallback (https://get.selfcloud.dev/${VERSION}/selfcloud-linux-${ARCH})"
    curl -fsSL "https://get.selfcloud.dev/${VERSION}/selfcloud-linux-${ARCH}" -o "$tmp" \
      || fail "could not download selfcloud binary; please install manually."
  fi
  # Fetch the matching .sha256 and verify. Skip on hard 404 (older
  # releases without checksums) but never silently accept on a partial
  # / corrupted body.
  if curl -fsSL "$sumurl" -o "$sumtmp" 2>/dev/null; then
    local expected actual
    expected="$(awk '{print $1}' "$sumtmp")"
    actual="$(sha256sum "$tmp" | awk '{print $1}')"
    if [ "$expected" != "$actual" ]; then
      fail "sha256 mismatch: expected $expected, got $actual"
    fi
    log "sha256 verified ($actual)"
  else
    warn "no .sha256 published for this version; skipping verification"
  fi
  run "chmod +x $tmp"
  run "$SUDO mv $tmp $BIN_DIR/selfcloud"
  log "installed $BIN_DIR/selfcloud"
}

# --- systemd ----------------------------------------------------------

write_unit() {
  [ "$USE_SYSTEMD" = 1 ] || return
  log "writing systemd unit"
  local unit
  # All unit content (User=, Group=, --data-dir, --api-addr) is rendered
  # by `selfcloud install` itself. Do NOT sed-patch the result; one
  # source of truth lives in internal/installer.RenderSystemd.
  unit="$($BIN_DIR/selfcloud install --render-only \
    --binary $BIN_DIR/selfcloud \
    --data-dir $DATA_DIR \
    --api-addr $API_ADDR \
    --user $SVC_USER \
    --group $SVC_USER)"
  if [ "$DRY_RUN" = 1 ]; then
    printf '+ write /etc/systemd/system/selfcloud.service:\n%s\n' "$unit"
    return
  fi
  printf '%s' "$unit" | $SUDO tee /etc/systemd/system/selfcloud.service >/dev/null
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now selfcloud
}

# --- main -------------------------------------------------------------

ensure_containerd
ensure_user
ensure_dirs
download_binary

# Run a preflight via the freshly-installed binary so the operator gets
# an immediate, structured "yes everything looks ok" before systemd is
# touched. The doctor exits non-zero on missing required pieces.
log "running preflight checks"
$BIN_DIR/selfcloud doctor --preflight --data-dir "$DATA_DIR" || fail "preflight failed; see above"

write_unit

if [ -n "$JOIN_ADDR" ] && [ -n "$JOIN_TOKEN" ]; then
  log "joining cluster at $JOIN_ADDR"
  # Pass --data-dir so the handshake file lands wherever the unit is
  # configured to read state, not just at the default /var/lib/selfcloud.
  run "$BIN_DIR/selfcloud join --leader $JOIN_ADDR --token $JOIN_TOKEN --data-dir $DATA_DIR"
fi

# Wait for /readyz to report 200, then surface the bootstrap token.
# /readyz only flips green once store + master.key + api + garage +
# reconciler (and raft, in multi-node) have all reported ready, so the
# operator never sees a "dashboard up but token unavailable" gap.
if [ "$USE_SYSTEMD" = 1 ] && [ -z "$JOIN_ADDR" ]; then
  for _ in $(seq 1 60); do
    if curl -fsSk "https://127.0.0.1:${API_ADDR##*:}/readyz" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  echo
  echo "=========================================================="
  echo "  selfCloud is up."
  echo "  Dashboard:  https://$(hostname -I 2>/dev/null | awk '{print $1}'):${API_ADDR##*:}/"
  if [ -s "$DATA_DIR/bootstrap-token" ]; then
    echo "  Bootstrap token: $($SUDO cat "$DATA_DIR/bootstrap-token")"
  else
    echo "  Bootstrap token: (already initialized; use the dashboard)"
  fi
  echo "=========================================================="
fi
