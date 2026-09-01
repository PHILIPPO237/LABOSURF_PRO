#!/usr/bin/env bash
set -Eeuo pipefail

# ============================================================
# LABOSURF PRO — Installateur Linux
# Laboratoire du FreeSurf • PHILIPPO237
# Premier moteur : UDP Engine
# ============================================================

APP_NAME="LABOSURF PRO"
APP_ID="labosurf"
INSTALL_DIR="/opt/labosurf"
CONFIG_DIR="/etc/labosurf"
BIN_PATH="/usr/local/bin/labosurf"
SERVICE_PATH="/etc/systemd/system/labosurf.service"
PUBKEY_PATH="${CONFIG_DIR}/license_pub.key"
ACTIVATION_PATH="${CONFIG_DIR}/activation.json"
MACHINE_PATH="${CONFIG_DIR}/machine.id"
LICENSE_REGISTRY_PATH="${CONFIG_DIR}/licenses.json"
GITHUB_REPO="PHILIPPO237/LABOSURF_PRO"
GITHUB_RELEASE="https://github.com/${GITHUB_REPO}/releases/latest/download"

# Palette terminal — sobre et lisible sur Android/Termius/SSH
RESET=$'\033[0m'; BOLD=$'\033[1m'; DIM=$'\033[2m'
GREEN=$'\033[1;32m'; CYAN=$'\033[1;36m'; BLUE=$'\033[1;34m'
YELLOW=$'\033[1;33m'; RED=$'\033[1;31m'; WHITE=$'\033[1;37m'

cleanup() { tput cnorm 2>/dev/null || true; printf '\033[0m'; }
trap cleanup EXIT INT TERM

term_width() {
  local w
  w="$(tput cols 2>/dev/null || true)"
  [[ "$w" =~ ^[0-9]+$ ]] || w=80
  printf '%s' "$w"
}

center() {
  local text="$1" width="$2" pad
  pad=$(( (width - ${#text}) / 2 ))
  (( pad < 0 )) && pad=0
  printf '%*s%s\n' "$pad" '' "$text"
}

banner() {
  clear 2>/dev/null || true
  local w
  w="$(term_width)"
  echo
  if (( w >= 52 )); then
    printf '%b' "$GREEN$BOLD"
    center '██╗      █████╗ ██████╗  ██████╗ ███████╗██╗   ██╗██████╗ ' "$w"
    center '██║     ██╔══██╗██╔══██╗██╔═══██╗██╔════╝██║   ██║██╔══██╗' "$w"
    center '██║     ███████║██████╔╝██║   ██║███████╗██║   ██║██████╔╝' "$w"
    center '██║     ██╔══██║██╔══██╗██║   ██║╚════██║██║   ██║██╔══██╗' "$w"
    center '███████╗██║  ██║██████╔╝╚██████╔╝███████║╚██████╔╝██║  ██║' "$w"
    printf '%b' "$RESET"
  else
    printf '%b' "$GREEN$BOLD"
    center 'LABOSURF PRO' "$w"
    printf '%b' "$RESET"
  fi
  center 'LABORATOIRE DU FREESURF' "$w"
  center 'CONÇU PAR PHILIPPO237  •  UDP ENGINE' "$w"
  echo
  center '──────────────────────────────────────' "$w"
  echo
}

info() { printf '  %b•%b %s\n' "$CYAN" "$RESET" "$*"; }
ok() { printf '  %b✔%b %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '  %b!%b %s\n' "$YELLOW" "$RESET" "$*"; }
die() { printf '\n  %b✖%b %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

spinner() {
  local pid="$1" msg="$2" frames='⠋⠙⠹⠸⠼⠴⠦⠧⠏⠋' i=0
  tput civis 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    printf '\r  %b%s%b %s' "$CYAN" "${frames:i++%10:1}" "$RESET" "$msg"
    sleep 0.08
  done
  wait "$pid"; local rc=$?
  if (( rc == 0 )); then
    printf '\r  %b✔%b %s\n' "$GREEN" "$msg"
  else
    printf '\r  %b✖%b %s\n' "$RED" "$msg"
  fi
  tput cnorm 2>/dev/null || true
  return "$rc"
}

run_step() {
  local msg="$1"; shift
  ( "$@" ) >/tmp/labosurf-install.$$ 2>&1 &
  local pid=$!
  if ! spinner "$pid" "$msg"; then
    sed -n '1,80p' /tmp/labosurf-install.$$ >&2 || true
    rm -f /tmp/labosurf-install.$$; die "Échec : $msg"
  fi
  rm -f /tmp/labosurf-install.$$
}

progress() {
  local n="$1" total="$2" label="$3" width=24 filled
  filled=$(( n * width / total ))
  local bar; bar="$(printf '%*s' "$filled" '' | tr ' ' '█')"
  local rest; rest="$(printf '%*s' "$((width-filled))" '' | tr ' ' '░')"
  printf '\n  %b[%d/%d]%b %b%s%b %b%s%s%b\n' "$DIM" "$n" "$total" "$RESET" "$WHITE" "$label" "$RESET" "$CYAN" "$bar" "$rest" "$RESET"
}

require_root() { [[ "$(id -u)" == 0 ]] || die "Lancez l'installateur avec sudo/root."; }

check_os() {
  [[ -r /etc/os-release ]] || die "Système Linux non reconnu."
  . /etc/os-release
  case "${ID:-}" in
    debian|ubuntu|linuxmint|raspbian) ;;
    *) warn "Distribution ${ID:-inconnue} non officiellement testée. Installation tentée avec apt." ;;
  esac
}

install_deps() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl wget openssl iptables
}

select_binary() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s' "labosurf-linux-amd64" ;;
    aarch64|arm64) printf '%s' "labosurf-linux-arm64" ;;
    *) return 1 ;;
  esac
}

download_binary() {
  local asset="$1" tmp="${BIN_PATH}.new"
  mkdir -p "$(dirname "$BIN_PATH")"
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 \
    "${GITHUB_RELEASE}/${asset}" -o "$tmp"
  chmod 0755 "$tmp"
  "$tmp" --help >/dev/null 2>&1
  install -m 0755 "$tmp" "$BIN_PATH"
  rm -f "$tmp"
}

fetch_public_key() {
  # La clé publique de production doit être fournie par la release officielle.
  # L'installateur ne génère jamais de clé privée et n'embarque aucune licence.
  local url="${GITHUB_RELEASE}/license_pub.key"
  local tmp="${PUBKEY_PATH}.new"
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 "$url" -o "$tmp"
  tr -d '[:space:]' < "$tmp" > "${tmp}.clean"
  mv "${tmp}.clean" "$tmp"
  [[ "$(wc -c < "$tmp")" -eq 64 ]] || die "Clé publique de release absente ou invalide. Publiez d'abord une release LABOSURF PRO contenant license_pub.key."
  install -m 0644 "$tmp" "$PUBKEY_PATH"
  rm -f "$tmp"
}

prepare_dirs() {
  install -d -m 0755 "$INSTALL_DIR" "$CONFIG_DIR"
  [[ -f "${CONFIG_DIR}/config.json" ]] || cat > "${CONFIG_DIR}/config.json" <<'JSON'
{
  "listen": ":5667",
  "store": "/etc/labosurf/users_db.json",
  "portal": {
    "enabled": true,
    "listen": ":8080"
  },
  "license": {
    "activation": "/etc/labosurf/activation.json",
    "machine_id": "/etc/labosurf/machine.id",
    "registry": "/etc/labosurf/licenses.json"
  },
  "tun": {
    "enabled": false,
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

activate_license() {
  local token
  echo
  printf '  %bLicence LABOSURF PRO%b\n' "$BOLD$CYAN" "$RESET"
  echo
  printf '  Entrez le jeton de licence : '
  IFS= read -r token < /dev/tty
  [[ -n "${token//[[:space:]]/}" ]] || die "Aucune licence fournie. Installation annulée."
  LABOSURF_LICENSE_PUBKEY="$(cat "$PUBKEY_PATH")" \
    "$BIN_PATH" license activate \
      -token "$token" \
      -activation "$ACTIVATION_PATH" \
      -machine "$MACHINE_PATH" \
      -registry "$LICENSE_REGISTRY_PATH"
}

install_service() {
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
  systemctl restart labosurf.service
}

install_menu_command() {
  cat > /usr/local/bin/menu <<'SH'
#!/usr/bin/env bash
exec /usr/local/bin/labosurf "$@"
SH
  chmod 0755 /usr/local/bin/menu
}

final_check() {
  "$BIN_PATH" license status -activation "$ACTIVATION_PATH" -machine "$MACHINE_PATH" >/dev/null 2>&1 || \
    die "L'activation n'a pas pu être vérifiée après installation."
  systemctl is-enabled labosurf.service >/dev/null
  systemctl is-active --quiet labosurf.service || { systemctl --no-pager --full status labosurf.service >&2 || true; die "LABOSURF PRO n'a pas démarré correctement."; }
}

main() {
  require_root
  banner
  printf '  %bInstallation de %s%b\n' "$BOLD$WHITE" "$APP_NAME" "$RESET"
  printf '  %bLaboratoire du FreeSurf • PHILIPPO237%b\n\n' "$DIM" "$RESET"

  progress 1 7 'Vérification du système'
  check_os
  ok 'Système Linux détecté.'

  progress 2 7 'Préparation des dépendances'
  run_step 'Installation des composants système...' install_deps

  progress 3 7 'Préparation des répertoires'
  run_step 'Création de l’environnement LABOSURF PRO...' prepare_dirs

  progress 4 7 'Déploiement du binaire'
  asset="$(select_binary)" || die "Architecture CPU non supportée : $(uname -m)"
  run_step "Téléchargement du binaire ${asset}..." download_binary "$asset"
  ok "Binaire LABOSURF PRO installé pour $(uname -m)."

  progress 5 7 'Sécurisation de la licence'
  run_step 'Installation de la clé publique de vérification...' fetch_public_key
  activate_license
  ok 'Licence vérifiée et activation enregistrée.'

  progress 6 7 'Configuration du service'
  run_step 'Configuration de systemd...' install_service
  run_step 'Installation de la commande menu...' install_menu_command

  progress 7 7 'Validation finale'
  run_step 'Vérification de l’installation...' final_check

  echo
  printf '  %b╭────────────────────────────────────────────╮%b\n' "$GREEN" "$RESET"
  printf '  %b│  ✔ LABOSURF PRO est installé              │%b\n' "$GREEN$BOLD" "$RESET"
  printf '  %b│  UDP Engine est prêt                      │%b\n' "$GREEN$BOLD" "$RESET"
  printf '  %b╰────────────────────────────────────────────╯%b\n' "$GREEN" "$RESET"
  echo
  info 'Commande principale : labosurf'
  info 'Menu administrateur : menu'
  info 'Service : systemctl status labosurf'
  info 'Configuration : /etc/labosurf/config.json'
  info 'Portail : selon la configuration du serveur'
  echo
  printf '  %bBienvenue dans LABOSURF PRO.%b\n' "$GREEN$BOLD" "$RESET"
}

main "$@"
