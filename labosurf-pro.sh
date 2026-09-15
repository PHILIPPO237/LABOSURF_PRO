#!/usr/bin/env bash
set -Eeuo pipefail

# ============================================================
# LABOSURF PRO — Professional Linux multi-engine installer
# Laboratoire du FreeSurf • PHILIPPO237
# Engines: UDP, Xray, SlowDNS, dnstt, Hysteria, Freeway-Gate, hybrids
# ============================================================
#
# ARCHITECTURE (see INSTALLER_PROFESSIONAL_UX_REPORT.md for the full
# rationale): the install is organized into 11 numbered, user-visible
# steps. License validation is step [2/11] — deliberately BEFORE any
# dependency installation, network change, or engine download — so an
# invalid/missing key stops everything before the system is touched
# beyond creating its own private config directory. The cryptographic
# verification itself (Ed25519, token format, activation window) is
# UNCHANGED from the previously audited system
# (AUDIT_LICENSE_COMPATIBILITY.md, AUDIT_INSTALL_LICENSE_GATE.md) —
# this file only changes presentation, step ordering, and honesty of
# the final health check, never the crypto or the license format.

APP_NAME="LABOSURF PRO"
APP_ID="labosurf"
INSTALL_DIR="/opt/labosurf"
CONFIG_DIR="/etc/labosurf"
BIN_PATH="/usr/local/bin/labosurf"
SERVICE_PATH="/etc/systemd/system/labosurf.service"
PUBKEY_PATH="${CONFIG_DIR}/license_pub.key"
RECEIPT_DIR="${CONFIG_DIR}"
GITHUB_REPO="PHILIPPO237/LABOSURF_PRO"
GITHUB_RELEASE="https://github.com/${GITHUB_REPO}/releases/latest/download"
TELEGRAM_CONTACT="https://t.me/Philippo237"
export BIN_PATH CONFIG_DIR GITHUB_REPO GITHUB_RELEASE

# Installer version banner: best-effort from the enclosing git checkout
# (dev/test use), falls back to "dev" for a standalone downloaded script
# (the normal curl|bash case) — never a fabricated version number.
# BASH_SOURCE is an EMPTY array when the script arrives via stdin (the
# documented `curl ... | sudo bash` install path, or `bash -s`), so
# BASH_SOURCE[0] must never be dereferenced unguarded under `set -u` —
# doing so aborted every curl|bash install with "unbound variable"
# before reaching the license screen.
INSTALLER_VERSION=""
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
  INSTALLER_VERSION="$(git -C "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)" describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')"
fi
[[ -n "$INSTALLER_VERSION" ]] || INSTALLER_VERSION="dev"

# Moteurs autonomes (binaires LABOSURF qui supervisent le vrai moteur tierce).
# chumo_engines : chaque binaire `labosurf-<name>` télécharge/déploie (SHA-256)
# puis supervise le moteur tierce sous systemd.
# Les moteurs hybrides sont créés dynamiquement depuis le menu central
# (composition libre de moteurs avec guide de compatibilité) ; ils ne sont
# donc pas énumérés ici. Binaires de moteurs principaux :
ENGINE_NAMES="xray slowdns dnstt hysteria tuic hysteria2 wireguard udp ssh freewaygate"

# ── Détection des capacités du terminal ────────────────────
IS_TTY_OUT=0
[[ -t 1 ]] && IS_TTY_OUT=1

if [[ "$IS_TTY_OUT" -eq 1 ]] && command -v tput &>/dev/null && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  RESET=$'\033[0m';  BOLD=$'\033[1m';   DIM=$'\033[2m'
  GREEN=$'\033[1;32m'; CYAN=$'\033[1;36m'; BLUE=$'\033[1;34m'
  YELLOW=$'\033[1;33m'; RED=$'\033[1;31m'; WHITE=$'\033[1;37m'
  UNDERLINE=$'\033[4m'
else
  RESET=''; BOLD=''; DIM=''
  GREEN=''; CYAN=''; BLUE=''
  YELLOW=''; RED=''; WHITE=''
  UNDERLINE=''
fi

# Détection Unicode : heuristique standard basée sur la locale (utilisée par
# de nombreux outils CLI — kubectl, oh-my-zsh, etc.). Un terminal qui annonce
# une locale non-UTF-8 (ou aucune) bascule sur un rendu ASCII pur : bordures,
# icônes de statut et spinner. Rien de spécifique à LABOSURF n'est deviné —
# c'est la même variable d'environnement que tout terminal Unix expose déjà.
UNICODE_OK=0
case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in
  *UTF-8*|*UTF8*|*utf-8*|*utf8*) UNICODE_OK=1 ;;
esac

if [[ "$UNICODE_OK" -eq 1 ]]; then
  ICON_OK='✓'; ICON_FAIL='✗'; ICON_WARN='!'; ICON_PENDING='○'; ICON_INFO='•'
  SPIN_FRAMES='⠋⠙⠹⠸⠼⠴⠦⠧⠏⠋'
  BOX_TL='╔'; BOX_TR='╗'; BOX_BL='╚'; BOX_BR='╝'; BOX_H='═'; BOX_V='║'
  LINE_TL='┌'; LINE_TR='┐'; LINE_BL='└'; LINE_BR='┘'; LINE_H='─'; LINE_V='│'
  SEP_CHAR='─'
  KEY_ICON='🔐'
  BULLET='•'
else
  ICON_OK='v'; ICON_FAIL='x'; ICON_WARN='!'; ICON_PENDING='.'; ICON_INFO='*'
  SPIN_FRAMES='-\|/'
  BOX_TL='+'; BOX_TR='+'; BOX_BL='+'; BOX_BR='+'; BOX_H='='; BOX_V='|'
  LINE_TL='+'; LINE_TR='+'; LINE_BL='+'; LINE_BR='+'; LINE_H='-'; LINE_V='|'
  SEP_CHAR='-'
  KEY_ICON='[KEY]'
  BULLET='-'
fi

# ── Nettoyage ──────────────────────────────────────────────
cleanup() { [[ "$IS_TTY_OUT" -eq 1 ]] && tput cnorm 2>/dev/null; printf '%b' "$RESET"; return 0; }
trap cleanup EXIT INT TERM

# ── Utilitaires terminal ───────────────────────────────────
term_width() {
  local w
  w="$(tput cols 2>/dev/null || true)"
  [[ "$w" =~ ^[0-9]+$ ]] || w=80
  printf '%s' "$w"
}

center() {
  local text="${1:-}" width="${2:-80}" pad
  pad=$(( (width - ${#text}) / 2 ))
  (( pad < 0 )) && pad=0
  printf '%*s%s\n' "$pad" '' "$text"
}

separator() {
  local w; w="$(term_width)"
  local width=$(( w > 78 ? 78 : w ))
  (( width < 10 )) && width=10
  printf '  %s\n' "$(printf -- "${SEP_CHAR}%.0s" $(seq 1 "$width"))"
}

# ── Box drawing (bordure simple — utilisée pour les écrans d'info) ──
box_line() {
  local content="${1:-}" width="${2:-80}"
  local inner=$((width - 4)); (( inner < 0 )) && inner=0
  local pad=$(( (inner - ${#content}) / 2 )); (( pad < 0 )) && pad=0
  local rpad=$(( inner - pad - ${#content} )); (( rpad < 0 )) && rpad=0
  printf '%s %*s%s%*s %s\n' "$LINE_V" "$pad" '' "$content" "$rpad" '' "$LINE_V"
}

box() {
  local w="${1:-80}"; shift
  local line; line="$(printf -- "${LINE_H}%.0s" $(seq 1 $((w - 2))))"
  printf '%s%s%s\n' "$LINE_TL" "$line" "$LINE_TR"
  for msg in "$@"; do box_line "$msg" "$w"; done
  printf '%s%s%s\n' "$LINE_BL" "$line" "$LINE_BR"
}

# ── Box drawing (bordure double — écrans "titre" : licence, fin) ──
double_box_line() {
  local content="${1:-}" width="${2:-80}"
  local inner=$((width - 4)); (( inner < 0 )) && inner=0
  local pad=$(( (inner - ${#content}) / 2 )); (( pad < 0 )) && pad=0
  local rpad=$(( inner - pad - ${#content} )); (( rpad < 0 )) && rpad=0
  printf '%s %*s%s%*s %s\n' "$BOX_V" "$pad" '' "$content" "$rpad" '' "$BOX_V"
}

double_box() {
  local w="${1:-80}"; shift
  local line; line="$(printf -- "${BOX_H}%.0s" $(seq 1 $((w - 2))))"
  printf '%s%s%s\n' "$BOX_TL" "$line" "$BOX_TR"
  for msg in "$@"; do double_box_line "$msg" "$w"; done
  printf '%s%s%s\n' "$BOX_BL" "$line" "$BOX_BR"
}

# ── Intro screen ───────────────────────────────────────────
print_intro() {
  clear 2>/dev/null || true
  local w
  w="$(term_width)"
  echo
  if [[ "$UNICODE_OK" -eq 1 ]] && (( w >= 52 )); then
    printf '%b' "$GREEN$BOLD"
    center '██╗      █████╗ ██████╗  ██████╗ ███████╗██╗   ██╗██████╗ ███████╗' "$w"
    center '██║     ██╔══██╗██╔══██╗██╔═══██╗██╔════╝██║   ██║██╔══██╗██╔════╝' "$w"
    center '██║     ███████║██████╔╝██║   ██║███████╗██║   ██║██████╔╝█████╗  ' "$w"
    center '██║     ██╔══██║██╔══██╗██║   ██║╚════██║██║   ██║██╔══██╗██╔══╝  ' "$w"
    center '███████╗██║  ██║██████╔╝╚██████╔╝███████║╚██████╔╝██║  ██║██║     ' "$w"
    printf '%b' "$RESET"
  else
    printf '%b' "$GREEN$BOLD"
    center 'LABOSURF PRO' "$w"
    printf '%b' "$RESET"
  fi
  center "${DIM}LABORATOIRE DU FREESURF${RESET}" "$w"
  center "${DIM}CONÇU PAR PHILIPPO237 ${BULLET} MULTI-MOTEURS${RESET}" "$w"
  echo
  center "${DIM}Installer v${INSTALLER_VERSION} ${BULLET} $(uname -s) $(uname -m)${RESET}" "$w"
  echo

  local box_w=50
  (( box_w > w - 2 )) && box_w=$((w - 2))
  printf '%b' "$CYAN"
  box "$box_w" "INSTALLATION" "Multi-engine ${BULLET} VPN"
  printf '%b' "$RESET"
  echo
}

# ── Plan d'installation (aperçu statique des 11 étapes) ────
STEP_LABELS=(
  "System Check"
  "License Validation"
  "Environment Preparation"
  "Dependencies"
  "Download Components"
  "Install Binaries"
  "Configure LABOSURF PRO"
  "Install Services"
  "Start Services"
  "Health Check"
  "Finalization"
)

print_plan() {
  printf '  %bInstallation plan:%b\n' "$DIM" "$RESET"
  local i=1 label
  for label in "${STEP_LABELS[@]}"; do
    printf '   %b[%s]%b [%d/%d] %s\n' "$DIM" "$ICON_PENDING" "$RESET" "$i" "$STEP_TOTAL" "$label"
    i=$((i + 1))
  done
  echo
}

# ── Indicateurs visuels ────────────────────────────────────
STEP_CURRENT=0
STEP_TOTAL=11
CURRENT_STEP_LABEL=""
COMPLETED_STEPS=()

mark_done() { COMPLETED_STEPS+=("$1"); }

step_begin() {
  STEP_CURRENT="$1"
  CURRENT_STEP_LABEL="$2"
  printf '\n  %b[%d/%d]%b %b%s%b\n' "$DIM" "$STEP_CURRENT" "$STEP_TOTAL" "$RESET" "$WHITE" "$CURRENT_STEP_LABEL" "$RESET"
}

step_spin() {
  local msg="$1"
  printf '  %b[▶]%b %s' "$CYAN" "$RESET" "$msg"
  [[ "$IS_TTY_OUT" -eq 1 ]] && tput civis 2>/dev/null || true
}

step_ok() {
  local msg="$1"
  printf '\r  %b[%s]%b %s\n' "$GREEN" "$ICON_OK" "$RESET" "$msg"
  [[ "$IS_TTY_OUT" -eq 1 ]] && tput cnorm 2>/dev/null
  return 0
}

step_fail() {
  local msg="$1"
  printf '\r  %b[%s]%b %s\n' "$RED" "$ICON_FAIL" "$RESET" "$msg"
  [[ "$IS_TTY_OUT" -eq 1 ]] && tput cnorm 2>/dev/null
  return 0
}

step_warn() {
  local msg="$1"
  printf '  %b[%s]%b %s\n' "$YELLOW" "$ICON_WARN" "$RESET" "$msg"
}

# ── Spinner animé (dégradé en simple attente si stdout n'est pas un tty) ──
spinner() {
  local pid="$1" msg="$2"

  if [[ "$IS_TTY_OUT" -eq 0 ]]; then
    # Pas de terminal : aucune animation par \r (polluerait un log/pipe).
    # L'opération continue normalement, juste sans rendu animé.
    wait "$pid"; local rc=$?
    if (( rc == 0 )); then
      printf '  [%s] %s\n' "$ICON_OK" "$msg"
    else
      printf '  [%s] %s\n' "$ICON_FAIL" "$msg"
    fi
    return "$rc"
  fi

  local frames="$SPIN_FRAMES" i=0 n=${#SPIN_FRAMES}
  tput civis 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    printf '\r  %b%s%b %s' "$CYAN" "${frames:i++%n:1}" "$RESET" "$msg"
    sleep 0.08
  done
  wait "$pid"; local rc=$?
  if (( rc == 0 )); then
    printf '\r  %b[%s]%b %s\n' "$GREEN" "$ICON_OK" "$RESET" "$msg"
  else
    printf '\r  %b[%s]%b %s\n' "$RED" "$ICON_FAIL" "$RESET" "$msg"
  fi
  tput cnorm 2>/dev/null || true
  return "$rc"
}

run_step() {
  local msg="$1"; shift
  ( "$@" ) >/tmp/labosurf-install.$$ 2>&1 &
  local pid=$!
  step_spin "$msg"
  if ! spinner "$pid" "$msg"; then
    if [[ -s /tmp/labosurf-install.$$ ]]; then
      printf '\n  %bDiagnostic output:%b\n' "$DIM" "$RESET" >&2
      sed -n '1,80p' /tmp/labosurf-install.$$ | sed 's/^/    /' >&2 || true
    fi
    rm -f /tmp/labosurf-install.$$
    die "Step failed: $msg"
  fi
  rm -f /tmp/labosurf-install.$$
}

# ── Messages ───────────────────────────────────────────────
info() { printf '  %b%s%b %s\n' "$CYAN" "$ICON_INFO" "$RESET" "$*"; }
ok()   { printf '  %b[%s]%b %s\n' "$GREEN" "$ICON_OK" "$RESET" "$*"; }
warn() { printf '  %b[%s]%b %s\n' "$YELLOW" "$ICON_WARN" "$RESET" "$*"; }

# die() : écran d'erreur structuré. N'affiche jamais de secret — "$*" est
# toujours une chaîne humaine fixe dans ce script, jamais un jeton de
# licence ou une clé (vérifié pour chaque appel de die() dans ce fichier).
die() {
  local msg="$*"
  echo >&2
  printf '  %b[%s]%b %bInstallation failed%b\n' "$RED" "$ICON_FAIL" "$RESET" "$RED$BOLD" "$RESET" >&2
  echo >&2
  if [[ -n "$CURRENT_STEP_LABEL" ]]; then
    printf '  Step   : [%d/%d] %s\n' "$STEP_CURRENT" "$STEP_TOTAL" "$CURRENT_STEP_LABEL" >&2
  fi
  printf '  Reason : %s\n' "$msg" >&2
  echo >&2
  exit 1
}

# ── Écran final ────────────────────────────────────────────
print_done() {
  local w
  w="$(term_width)"
  echo
  local box_w=56
  (( box_w > w - 2 )) && box_w=$((w - 2))
  printf '%b' "$GREEN$BOLD"
  double_box "$box_w" "LABOSURF PRO INSTALLATION" "COMPLETED"
  printf '%b' "$RESET"
  echo

  local item
  for item in "${COMPLETED_STEPS[@]}"; do
    printf '  %b[%s]%b %s\n' "$GREEN" "$ICON_OK" "$RESET" "$item"
  done
  echo
  printf '  %bLABOSURF PRO is ready.%b\n' "$GREEN$BOLD" "$RESET"
  echo

  info "Main command       : ${BOLD}labosurf${RESET}"
  info "Standalone engines : ${BOLD}labosurf-<engine>${RESET} (xray, slowdns, dnstt, hysteria, ...)"
  info "Admin menu         : ${BOLD}menu${RESET}"
  info "Core service       : ${BOLD}systemctl status labosurf${RESET}"
  info "Engine configs     : ${BOLD}/etc/labosurf/engines/<engine>.conf${RESET}"
  info "HTTP portal        : ${BOLD}http://<IP>:8080${RESET}"
  echo
}

# ── Écran de validation de licence ─────────────────────────
# Purement présentationnel : le contact Telegram n'est qu'une information
# pour obtenir une clé auprès de l'administrateur. Aucune activation ne
# passe par Telegram — la seule voie d'autorisation reste la vérification
# cryptographique Ed25519 faite plus bas dans activate_license().
print_license_screen() {
  local w; w="$(term_width)"
  local box_w=56
  (( box_w > w - 2 )) && box_w=$((w - 2))

  echo
  printf '%b' "$CYAN$BOLD"
  double_box "$box_w" "LABOSURF PRO" "LICENSE VALIDATION"
  printf '%b' "$RESET"
  echo
  printf '  %s %bEnter your LABOSURF PRO activation key:%b\n' "$KEY_ICON" "$BOLD" "$RESET"
  echo
  separator
  printf '  %bNo activation key yet?%b\n' "$DIM" "$RESET"
  printf '  Contact the administrator to obtain your activation key.\n'
  echo
  printf '  Telegram:\n'
  printf '  %b%s%b\n' "$CYAN$UNDERLINE" "$TELEGRAM_CONTACT" "$RESET"
  separator
  echo
}

# ── Fonctions d'installation (LOGIQUE CRYPTO/RÉSEAU INCHANGÉE) ──

require_root() { [[ "$(id -u)" == 0 ]] || die "Run the installer with sudo/root."; }

check_os() {
  [[ -r /etc/os-release ]] || die "Unrecognized Linux system."
  . /etc/os-release
  case "${ID:-}" in
    debian|ubuntu|linuxmint|raspbian) ;;
    *) warn "Distribution ${ID:-unknown} is not officially tested. Attempting installation with apt." ;;
  esac
}

install_deps() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl wget openssl iptables coreutils openssh-server
}

setup_network() {
  # Charger le module TUN
  modprobe tun 2>/dev/null || true

  # Persister l'IPv4 forwarding (appliqué aussi au runtime par le serveur Go)
  sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
  if ! grep -q 'net.ipv4.ip_forward=1' /etc/sysctl.conf 2>/dev/null; then
    echo 'net.ipv4.ip_forward=1' >> /etc/sysctl.conf
  fi

  # Détecter l'interface WAN (route par défaut) — NE PAS supposer eth0
  local wan_if
  wan_if="$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')"
  if [[ -z "$wan_if" ]]; then
    # Fallback : première interface non-lo avec une adresse IPv4 globale
    wan_if="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $2; exit}')"
  fi
  if [[ -z "$wan_if" ]]; then
    warn "WAN interface not detected. NAT will be configured by the server at startup."
    wan_if=""
  else
    info "WAN interface detected: $wan_if"
  fi

  # Ouvrir les ports UDP/TCP nécessaires dans le firewall local.
  # Le NAT et le FORWARD détaillés sont configurés au runtime par le serveur Go
  # (network.go) qui détecte dynamiquement l'interface WAN à chaque démarrage.
  if command -v iptables &>/dev/null; then
    iptables -C INPUT -p udp --dport 5667 -j ACCEPT 2>/dev/null || \
      iptables -I INPUT -p udp --dport 5667 -j ACCEPT
    iptables -C INPUT -p tcp --dport 8080 -j ACCEPT 2>/dev/null || \
      iptables -I INPUT -p tcp --dport 8080 -j ACCEPT
  fi

  # Si nftables est présent, autoriser aussi via nft (certains VPS n'ont que nft)
  if command -v nft &>/dev/null && ! command -v iptables &>/dev/null; then
    nft add table inet labosurf-filter 2>/dev/null || true
    nft add chain inet labosurf-filter input '{ type filter hook input priority 0; policy accept; }' 2>/dev/null || true
    nft add rule inet labosurf-filter input udp dport 5667 accept 2>/dev/null || true
    nft add rule inet labosurf-filter input tcp dport 8080 accept 2>/dev/null || true
  fi
}

# arch_suffix retourne le suffixe de nom d'asset selon l'architecture.
arch_suffix() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s' "linux-amd64" ;;
    aarch64|arm64) printf '%s' "linux-arm64" ;;
    *) return 1 ;;
  esac
}

# download_asset télécharge et vérifie (SHA-256) un asset LABOSURF de la
# release, puis l'installe à la destination donnée.
# Usage : download_asset <baseAssetName> <destinationPath>
download_asset() {
  local base="$1" dest="$2"
  local suffix asset tmp="${dest}.new" sums="${dest}.sums"
  local expected actual
  mkdir -p "$(dirname "$dest")"
  suffix="$(arch_suffix)" || { rm -f "$tmp" "$sums"; die "Unsupported CPU architecture: $(uname -m)"; }
  asset="${base}-${suffix}"

  # 1. Télécharger le manifeste SHA256SUMS complet produit par le workflow.
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 \
    "${GITHUB_RELEASE}/SHA256SUMS" -o "$sums" \
    || { rm -f "$sums"; die "SHA256SUMS not found in the release. Publish a complete LABOSURF PRO release first."; }

  # 2. Extraire le hash attendu pour cet asset.
  expected="$(awk -v a="$asset" '$2 == a {print $1}' "$sums")"
  [[ -n "$expected" ]] \
    || { rm -f "$tmp" "$sums"; die "No SHA-256 hash for ${asset} in SHA256SUMS — inconsistent release."; }

  # 3. Télécharger le binaire correspondant.
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 \
    "${GITHUB_RELEASE}/${asset}" -o "$tmp" \
    || { rm -f "$tmp" "$sums"; die "Asset ${asset} not found in the release."; }

  # 4. Vérification d'intégrité : refuse tout binaire altéré ou corrompu.
  actual="$(sha256sum "$tmp" | awk '{print $1}')"
  rm -f "$sums"
  [[ "$actual" == "$expected" ]] \
    || { rm -f "$tmp"; die "SHA-256 mismatch for ${asset} — installation aborted (corrupted or tampered file)."; }

  # 5. Smoke test : le binaire doit réellement s'exécuter sur ce système.
  chmod 0755 "$tmp"
  "$tmp" --help >/dev/null 2>&1 \
    || { rm -f "$tmp"; die "Downloaded binary does not run on this system."; }

  install -m 0755 "$tmp" "$dest"
  rm -f "$tmp"
}

fetch_public_key() {
  local url="${GITHUB_RELEASE}/license_pub.key"
  local tmp="${PUBKEY_PATH}.new"
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 "$url" -o "$tmp" \
    || { rm -f "$tmp"; die "Public key not found in the release. Publish a LABOSURF PRO release containing license_pub.key first."; }
  tr -d '[:space:]' < "$tmp" > "${tmp}.clean"
  mv "${tmp}.clean" "$tmp"
  # Une clé publique Ed25519 valide = exactement 64 caractères hexadécimaux.
  # (la page 404 de GitHub dépasse 64 octets : la taille seule ne suffit pas)
  [[ "$(wc -c < "$tmp")" -eq 64 && "$(cat "$tmp")" =~ ^[0-9a-fA-F]{64}$ ]] \
    || { rm -f "$tmp"; die "Release public key missing or invalid (64 hex characters expected)."; }
  install -m 0644 "$tmp" "$PUBKEY_PATH"
  rm -f "$tmp"
}

# prepare_dirs crée uniquement les répertoires nécessaires (y compris pour
# accueillir license_pub.key avant même la validation de licence). La
# génération du contenu de configuration est séparée dans
# write_default_config() — voir étape [7/11] Configure LABOSURF PRO.
prepare_dirs() {
  install -d -m 0755 "$INSTALL_DIR" "$CONFIG_DIR" "${CONFIG_DIR}/engines"
}

write_default_config() {
  [[ -f "${CONFIG_DIR}/config.json" ]] || cat > "${CONFIG_DIR}/config.json" <<'JSON'
{
  "listen": ":5667",
  "store": "/etc/labosurf/users_db.json",
  "portal": {
    "enabled": true,
    "listen": ":8080"
  },
  "license": {
    "receipt_dir": "/etc/labosurf"
  },
  "tun": {
    "enabled": true,
    "name": "labosurf0",
    "address": "10.77.0.1/24"
  },
  "auth": {
    "mode": "passwords",
    "users": {}
  }
}
JSON
  [[ -f "${CONFIG_DIR}/users_db.json" ]] || printf '{}\n' > "${CONFIG_DIR}/users_db.json"
  chmod 0600 "${CONFIG_DIR}/users_db.json"
}

# read_license_token lit le jeton saisi par l'opérateur sur le terminal
# contrôlant (/dev/tty, pas stdin — cohérent avec select_engines() plus bas,
# nécessaire car ce script peut être exécuté via `curl | bash`, où stdin est
# déjà consommé par le flux de téléchargement). Isolée dans sa propre
# fonction uniquement pour permettre à un test de sourcer ce script et de la
# redéfinir (test_install_license_gate.sh) sans dépendre d'un vrai terminal
# — le comportement en usage réel (installateur exécuté normalement) est
# inchangé. Si aucun terminal contrôlant n'existe (environnement totalement
# non interactif), la lecture échoue proprement et le jeton reste vide —
# l'installation se bloque alors normalement (échec fermé, jamais ouvert).
read_license_token() {
  local t=""
  IFS= read -r t < /dev/tty 2>/dev/null || true
  printf '%s' "$t"
}

activate_license() {
  # La licence ouvre l'ACCÈS AU SCRIPT D'INSTALLATION (1 clé = 1 installation).
  # Vérifiée une seule fois ici : signature Ed25519 + fenêtre de 3h.
  # Le serveur tournera ensuite librement, sans contrôle de licence.
  #
  # AUCUN contournement n'existe ici (ni variable d'environnement, ni
  # fichier, ni option cachée) : toute installation complète nécessite un
  # jeton qui passe réellement la vérification cryptographique Ed25519
  # ci-dessous. Un ancien flag LABOSURF_DEV=1 sautait entièrement cette
  # étape ; il a été retiré (AUDIT_INSTALL_LICENSE_GATE.md) car ce script
  # est distribué publiquement — un tel flag, visible par quiconque lit le
  # script, n'offre aucune protection réelle contre un utilisateur final.
  # Cette fonction ne fait QUE de la présentation par rapport à la version
  # précédente : la logique cryptographique ci-dessous est strictement
  # identique (voir AUDIT_INSTALL_LICENSE_GATE.md).
  local token id
  print_license_screen
  printf '  %b1 key = 1 installation %s valid for 3 hours after issuance%b\n' "$DIM" "$BULLET" "$RESET"
  echo
  printf '  %b>%b ' "$CYAN" "$RESET"
  token="$(read_license_token)"
  echo
  [[ -n "${token//[[:space:]]/}" ]] || die "No activation key provided. Installation cancelled."
  id="$(LABOSURF_LICENSE_PUBKEY="$(cat "$PUBKEY_PATH")" \
    "$BIN_PATH" license verify -token "$token" -print-id)" \
    || die "Activation key rejected (invalid signature or 3-hour validation window expired)."
  [[ -n "$id" ]] || die "Activation key rejected (unreadable identifier)."
  local safe_id; safe_id="$(printf '%s' "$id" | tr -c 'A-Za-z0-9_-' '_')"
  [[ -n "$safe_id" ]] || safe_id="unknown"
  if [[ -f "${RECEIPT_DIR}/.install_${safe_id}.receipt" ]]; then
    die "This license already authorized an installation (1 key = 1 install). Request a NEW license."
  fi
  printf '{"license_id":"%s","installed_at":"%s"}\n' "$id" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    > "${RECEIPT_DIR}/.install_${safe_id}.receipt.tmp"
  chmod 0600 "${RECEIPT_DIR}/.install_${safe_id}.receipt.tmp"
  mv "${RECEIPT_DIR}/.install_${safe_id}.receipt.tmp" "${RECEIPT_DIR}/.install_${safe_id}.receipt"
}

# install_service_unit écrit et active (sans démarrer) le service systemd
# central. Le démarrage réel est fait séparément par start_core_service()
# — étape [9/11] Start Services — pour distinguer "installé" de "démarré"
# dans l'expérience utilisateur.
install_service_unit() {
  cat > "$SERVICE_PATH" <<'UNIT'
[Unit]
Description=LABOSURF PRO — UDP Engine
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/labosurf
ExecStart=/usr/local/bin/labosurf udp server -c /etc/labosurf/config.json
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=/etc/labosurf

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$SERVICE_PATH"
  systemctl daemon-reload
  systemctl enable labosurf.service >/dev/null
}

start_core_service() {
  systemctl restart labosurf.service
}

# ── Sélection interactive des moteurs à installer ──────────
select_engines() {
  # ENV override : LABOSURF_ENGINES="xray,hysteria"
  local override="${LABOSURF_ENGINES:-}"
  if [[ -n "$override" ]]; then
    SELECTED_ENGINES="${override//,/ }"
    SELECTED_ENGINES="${SELECTED_ENGINES// /}"
    return
  fi

  info "Available engines: $ENGINE_NAMES"
  echo
  printf '  Enter engines to install (space-separated, "all" for all): '
  IFS= read -r SELECTED_ENGINES < /dev/tty
}

# ── Déploiement d'un binaire moteur autonome ──────────────
install_engine_binary() {
  local eng="$1"
  local dest="/usr/local/bin/labosurf-${eng}"
  download_asset "labosurf-${eng}" "$dest"
}

# ── Service systemd par moteur (supervision du vrai binaire tierce) ──
install_engine_service() {
  local eng="$1"
  local eng_service="/etc/systemd/system/labosurf-${eng}.service"
  local engconf="/etc/labosurf/engines/${eng}.conf"
  cat > "$eng_service" <<UNIT
[Unit]
Description=LABOSURF PRO — Moteur ${eng}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
EnvironmentFile=${engconf}
ExecStart=/usr/local/bin/labosurf-${eng} run
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$eng_service"
}

enable_engine_service() {
  systemctl enable "labosurf-$1.service" >/dev/null
}

# Fichier de configuration d'environnement d'un moteur (source du binaire tierce).
gen_engine_conf() {
  local eng="$1"
  local conf="/etc/labosurf/engines/${eng}.conf"
  if [[ ! -f "$conf" ]]; then
    cat > "$conf" <<CONF
# Configuration du moteur ${eng} — préfixe d'environnement LABOSURF_${eng^^}
# Renseignez l'URL et l'empreinte SHA-256 du vrai moteur tierce :
#LABOSURF_${eng^^}_BINARY_URL=https://github.com/.../releases/download/v1.0/xray-linux-amd64.zip
#LABOSURF_${eng^^}_BINARY_SHA256=...
CONF
    chmod 0600 "$conf"
  fi
}

# ── Installation du binaire tierce via le wrapper autonome ──
install_engine_thirdparty() {
  local eng="$1"
  # Le wrapper télécharge/déploie le vrai moteur, puis on le teste.
  # Un échec ici n'est qu'un avertissement (le provisionnement tierce peut
  # être complété plus tard par l'opérateur) — mais il n'est plus silencieux
  # comme avant : final_check(), à l'étape [10/11] Health Check, vérifie
  # désormais réellement que le service de CHAQUE moteur sélectionné tourne
  # (systemctl is-active), et fait échouer l'installation avec un message
  # clair si ce n'est pas le cas — voir INSTALLER_PROFESSIONAL_UX_REPORT.md.
  if ! /usr/local/bin/labosurf-${eng} install; then
    warn "Engine ${eng}: third-party source not provisioned (URL/SHA-256 missing in ${CONFIG_DIR}/engines/${eng}.conf)."
  fi
}

# ── Utilisateur système `labosurf` + authorized_keys du moteur SSH ──
# Le moteur Go écrit authorized_keys dans ${CONFIG_DIR}/ssh/authorized_keys
# ; on crée l'utilisateur système qui sert de cible aux connexions SSH et
# on y symlink ce fichier (AllowUsers labosurf via sshd_config).
install_ssh_user() {
  local ssh_dir="${CONFIG_DIR}/ssh" home="/home/labosurf"
  install -d -m 0755 "$ssh_dir"
  [[ -f "${ssh_dir}/authorized_keys" ]] || : > "${ssh_dir}/authorized_keys"
  chmod 0600 "${ssh_dir}/authorized_keys"

  if ! id -u labosurf &>/dev/null; then
    useradd --create-home --shell /bin/bash --home-dir "$home" labosurf
  fi
  install -d -m 0700 "$home/.ssh"
  rm -f "$home/.ssh/authorized_keys"
  ln -sf "${ssh_dir}/authorized_keys" "$home/.ssh/authorized_keys"
  chown -R labosurf:labosurf "$home/.ssh" "$ssh_dir"
}

install_menu_command() {
  cat > /usr/local/bin/menu <<'SH'
#!/usr/bin/env bash
exec /usr/local/bin/labosurf "$@"
SH
  chmod 0755 /usr/local/bin/menu
}

# final_check() — Health Check honnête : vérifie le reçu de licence, le
# service central, ET (nouveau) chaque service moteur réellement
# sélectionné. Avant cette session, seul le service central était vérifié
# (limitation déjà documentée : AUDIT_TECHNIQUE_COMPLET_2026-09-09.md §8,
# "la validation finale ne couvre pas les moteurs sélectionnés" — un moteur
# tiers mal provisionné pouvait être rapporté comme installé avec succès).
# Corrigé ici car cette mission exige explicitement de ne jamais afficher
# un succès pour une opération qui a réellement échoué.
final_check() {
  "$BIN_PATH" license status -receipt-dir "$RECEIPT_DIR" >/dev/null 2>&1 || \
    die "Install receipt not found after installation."
  systemctl is-enabled labosurf.service >/dev/null
  systemctl is-active --quiet labosurf.service || {
    systemctl --no-pager --full status labosurf.service >&2 || true
    die "LABOSURF PRO core service did not start correctly."
  }

  local eng
  for eng in "$@"; do
    systemctl is-active --quiet "labosurf-${eng}.service" || {
      systemctl --no-pager --full status "labosurf-${eng}.service" >&2 || true
      die "Engine service labosurf-${eng} did not start correctly."
    }
  done
}

# ── Point d'entrée ─────────────────────────────────────────
main() {
  require_root
  print_intro
  print_plan

  # ── [1/11] System Check ────────────────────────────────
  step_begin 1 "${STEP_LABELS[0]}"
  check_os
  step_ok "System check passed ($(source /etc/os-release && echo "${PRETTY_NAME:-$ID}"), $(uname -m))"
  mark_done "System check passed"

  # ── [2/11] License Validation ──────────────────────────
  # Volontairement TRÈS TÔT : avant toute dépendance, tout changement
  # réseau, tout téléchargement de moteur. Seuls les répertoires privés de
  # LABOSURF PRO sont créés (nécessaires pour recevoir license_pub.key et
  # le binaire vérificateur) et le vérificateur lui-même est téléchargé —
  # rien d'autre n'est modifié sur le système tant que la clé n'est pas
  # validée.
  step_begin 2 "${STEP_LABELS[1]}"
  run_step 'Preparing environment...' prepare_dirs
  run_step 'Downloading validator...' download_asset "labosurf" "$BIN_PATH"
  run_step 'Fetching verification key...' fetch_public_key
  activate_license
  step_ok "License validated"
  mark_done "License validated"

  # ── [3/11] Environment Preparation ─────────────────────
  step_begin 3 "${STEP_LABELS[2]}"
  run_step 'Configuring network (TUN, NAT, forwarding)...' setup_network
  step_ok "Environment ready"
  mark_done "Environment prepared"

  # ── [4/11] Dependencies ─────────────────────────────────
  step_begin 4 "${STEP_LABELS[3]}"
  run_step 'Installing system packages...' install_deps
  step_ok "Dependencies installed"
  mark_done "Dependencies installed"

  # ── [5/11] Download Components ─────────────────────────
  step_begin 5 "${STEP_LABELS[4]}"
  select_engines
  info "Selected engines: ${SELECTED_ENGINES}"
  local -a VALID_ENGINES=()
  local eng
  for eng in $SELECTED_ENGINES; do
    case " $ENGINE_NAMES " in
      *" $eng "*) VALID_ENGINES+=("$eng") ;;
      *) warn "Unknown engine ignored: $eng" ;;
    esac
  done
  for eng in "${VALID_ENGINES[@]}"; do
    run_step "Downloading ${eng} component..." install_engine_binary "$eng"
  done
  step_ok "Components downloaded"

  # ── [6/11] Install Binaries ─────────────────────────────
  step_begin 6 "${STEP_LABELS[5]}"
  for eng in "${VALID_ENGINES[@]}"; do
    run_step "Preparing ${eng} service definition..." install_engine_service "$eng"
    run_step "Generating ${eng} configuration..." gen_engine_conf "$eng"
    if [[ "$eng" == "ssh" ]]; then
      run_step "Provisioning SSH system user..." install_ssh_user
    fi
  done
  step_ok "Binaries installed"
  mark_done "Components installed"

  # ── [7/11] Configure LABOSURF PRO ──────────────────────
  step_begin 7 "${STEP_LABELS[6]}"
  run_step 'Writing default configuration...' write_default_config
  run_step 'Installing menu command...' install_menu_command
  step_ok "Configuration completed"
  mark_done "Configuration completed"

  # ── [8/11] Install Services ────────────────────────────
  step_begin 8 "${STEP_LABELS[7]}"
  run_step 'Installing core systemd service...' install_service_unit
  if (( ${#VALID_ENGINES[@]} > 0 )); then
    systemctl daemon-reload
  fi
  for eng in "${VALID_ENGINES[@]}"; do
    run_step "Enabling ${eng} service..." enable_engine_service "$eng"
    run_step "Provisioning ${eng} third-party binary..." install_engine_thirdparty "$eng"
  done
  step_ok "Services installed"
  mark_done "Services installed"

  # ── [9/11] Start Services ──────────────────────────────
  step_begin 9 "${STEP_LABELS[8]}"
  run_step 'Starting core service...' start_core_service
  for eng in "${VALID_ENGINES[@]}"; do
    run_step "Starting ${eng} service..." systemctl restart "labosurf-${eng}.service"
  done
  step_ok "Services started"
  mark_done "Services started"

  # ── [10/11] Health Check ───────────────────────────────
  step_begin 10 "${STEP_LABELS[9]}"
  run_step "Checking installation health..." final_check "${VALID_ENGINES[@]}"
  step_ok "Health checks passed"
  mark_done "Health checks passed"

  # ── [11/11] Finalization ───────────────────────────────
  step_begin 11 "${STEP_LABELS[10]}"
  step_ok "Finalizing installation"
  print_done
}

# Exécute main() seulement si le script est lancé directement (bash
# labosurf-pro.sh, ./labosurf-pro.sh, curl | bash) — comportement inchangé
# pour tout utilisateur réel. Si le script est *sourcé* (source
# labosurf-pro.sh depuis test_install_license_gate.sh), main() ne se lance
# pas automatiquement : le test peut alors appeler activate_license
# directement, en isolation, sans lancer une installation complète.
#
# L'ancien garde comparait "${BASH_SOURCE[0]}" à "${0}" : en plus de
# planter (BASH_SOURCE[0] non défini sous `set -u` quand le script vient
# d'un pipe), la comparaison aurait de toute façon échoué une fois
# "corrigée" naïvement — pour `curl ... | sudo bash`, BASH_SOURCE[0] est
# une chaîne vide alors que $0 vaut "bash", donc main() ne se serait
# jamais lancé. L'idiome `(return 0 2>/dev/null)` détecte correctement
# le sourcing sans dépendre de BASH_SOURCE : `return` hors fonction ou
# script sourcé échoue, qu'il s'agisse d'un fichier ou d'un flux stdin.
if ! (return 0 2>/dev/null); then
  main "$@"
fi
