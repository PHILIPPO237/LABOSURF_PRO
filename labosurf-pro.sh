#!/usr/bin/env bash
set -Eeuo pipefail

# ============================================================
# LABOSURF PRO — Installateur Linux multi-moteurs
# Laboratoire du FreeSurf • PHILIPPO237
# Moteurs : UDP, Xray, SlowDNS, dnstt, Hysteria, hybrides
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

# Moteurs autonomes (binaires LABOSURF qui supervisent le vrai moteur tierce).
# chumo_engines : chaque binaire `labosurf-<name>` télécharge/déploie (SHA-256)
# puis supervise le moteur tierce sous systemd.
# Les moteurs hybrides sont créés dynamiquement depuis le menu central
# (composition libre de moteurs avec guide de compatibilité) ; ils ne sont
# donc pas énumérés ici. Binaires de moteurs principaux :
ENGINE_NAMES="xray slowdns dnstt hysteria udp ssh"

# ── Détection couleur ──────────────────────────────────────
if [[ -t 1 ]] && command -v tput &>/dev/null && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
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

# ── Nettoyage ──────────────────────────────────────────────
cleanup() { tput cnorm 2>/dev/null || true; printf '%b' "$RESET"; }
trap cleanup EXIT INT TERM

# ── Utilitaires terminal ───────────────────────────────────
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

# ── Box drawing ────────────────────────────────────────────
box_line() {
  local content="$1" width="$2" inner=$((width - 4))
  local pad=$(( (inner - ${#content}) / 2 ))
  (( pad < 0 )) && pad=0
  local rpad=$(( inner - pad - ${#content} ))
  (( rpad < 0 )) && rpad=0
  printf '│ %*s%s%*s │\n' "$pad" '' "$content" "$rpad" ''
}

box() {
  local w="$1"; shift
  local line
  line="$(printf '─%.0s' $(seq 1 $((w - 4))))"
  printf '┌%s┐\n' "$line"
  for msg in "$@"; do
    box_line "$msg" "$w"
  done
  printf '└%s┘\n' "$line"
}

# ── Intro screen ───────────────────────────────────────────
print_intro() {
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
  center "${DIM}LABORATOIRE DU FREESURF${RESET}" "$w"
  center "${DIM}CONÇU PAR PHILIPPO237 • MULTI-MOTEURS${RESET}" "$w"
  echo

  local box_w=50
  (( box_w > w - 2 )) && box_w=$((w - 2))
  printf '%b' "$CYAN"
  box "$box_w" "INSTALLATION" "Multi-moteurs • VPN" ""
  printf '%b' "$RESET"
  echo
}

# ── Indicateurs visuels ────────────────────────────────────
STEP_CURRENT=0
STEP_TOTAL=9

step_begin() {
  STEP_CURRENT="$1"
  local label="$2"
  printf '\n  %b[%d/%d]%b %b%s%b\n' "$DIM" "$STEP_CURRENT" "$STEP_TOTAL" "$RESET" "$WHITE" "$label" "$RESET"
}

step_spin() {
  local msg="$1"
  printf '  %b[▶]%b %s' "$CYAN" "$RESET" "$msg"
  tput civis 2>/dev/null || true
}

step_ok() {
  local msg="$1"
  printf '\r  %b[✓]%b %s\n' "$GREEN" "$RESET" "$msg"
  tput cnorm 2>/dev/null || true
}

step_fail() {
  local msg="$1"
  printf '\r  %b[✗]%b %s\n' "$RED" "$RESET" "$msg"
  tput cnorm 2>/dev/null || true
}

step_warn() {
  local msg="$1"
  printf '  %b[!]%b %s\n' "$YELLOW" "$RESET" "$msg"
}

# ── Spinner animé ──────────────────────────────────────────
spinner() {
  local pid="$1" msg="$2" frames='⠋⠙⠹⠸⠼⠴⠦⠧⠏⠋' i=0
  tput civis 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    printf '\r  %b%s%b %s' "$CYAN" "${frames:i++%10:1}" "$RESET" "$msg"
    sleep 0.08
  done
  wait "$pid"; local rc=$?
  if (( rc == 0 )); then
    printf '\r  %b[✓]%b %s\n' "$GREEN" "$RESET" "$msg"
  else
    printf '\r  %b[✗]%b %s\n' "$RED" "$RESET" "$msg"
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
    sed -n '1,80p' /tmp/labosurf-install.$$ >&2 || true
    rm -f /tmp/labosurf-install.$$; die "Échec : $msg"
  fi
  rm -f /tmp/labosurf-install.$$
}

# ── Barre de progression ───────────────────────────────────
progress_bar() {
  local label="$1" current="$2" total="$3"
  local width=24 filled pct
  filled=$(( current * width / total ))
  pct=$(( current * 100 / total ))
  local bar; bar="$(printf '%*s' "$filled" '' | tr ' ' '█')"
  local rest; rest="$(printf '%*s' "$((width-filled))" '' | tr ' ' '░')"
  printf '\r  %b[▶]%b %s %b%s%s%b %d%%' "$CYAN" "$RESET" "$label" "$CYAN" "$bar" "$rest" "$pct"
}

# ── Messages ───────────────────────────────────────────────
info() { printf '  %b•%b %s\n' "$CYAN" "$RESET" "$*"; }
ok()   { printf '  %b✔%b %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '  %b[!]%b %s\n' "$YELLOW" "$RESET" "$*"; }
die()  { printf '\n  %b[✗]%b %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

# ── Écran final ────────────────────────────────────────────
print_done() {
  local w
  w="$(term_width)"
  echo
  local box_w=50
  (( box_w > w - 2 )) && box_w=$((w - 2))
  printf '%b' "$GREEN$BOLD"
  box "$box_w" "INSTALLATION TERMINEE" "LABOSURF PRO pret" ""
  printf '%b' "$RESET"
  echo
  info "Commande principale : ${BOLD}labosurf${RESET}"
  info "Moteurs autonomes     : ${BOLD}labosurf-<engine>${RESET} (xray, slowdns, dnstt, hysteria, ...)"
  info "Menu administrateur : ${BOLD}menu${RESET}"
  info "Service UDP          : ${BOLD}systemctl status labosurf${RESET}"
  info "Config moteurs       : ${BOLD}/etc/labosurf/engines/<engine>.conf${RESET}"
  info "Portail HTTP         : ${BOLD}http://<IP>:8080${RESET}"
  echo
  printf '  %bBienvenue dans LABOSURF PRO.%b\n' "$GREEN$BOLD" "$RESET"
  echo
}

# ── Fonctions d'installation (LOGIQUE INCHANGÉE) ──────────

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
    warn "Interface WAN non détectée. Le NAT sera configuré par le serveur au démarrage."
    wan_if=""
  else
    info "Interface WAN détectée : $wan_if"
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
  local base="$1" dest="$2" suffix asset tmp="${dest}.new" sums="${dest}.sums"
  local expected actual
  mkdir -p "$(dirname "$dest")"
  suffix="$(arch_suffix)" || { rm -f "$tmp" "$sums"; die "Architecture CPU non supportée : $(uname -m)"; }
  asset="${base}-${suffix}"

  # 1. Télécharger le manifeste SHA256SUMS complet produit par le workflow.
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 \
    "${GITHUB_RELEASE}/SHA256SUMS" -o "$sums" \
    || { rm -f "$sums"; die "SHA256SUMS introuvable dans la release. Publiez d'abord une release LABOSURF PRO complète."; }

  # 2. Extraire le hash attendu pour cet asset.
  expected="$(awk -v a="$asset" '$2 == a {print $1}' "$sums")"
  [[ -n "$expected" ]] \
    || { rm -f "$tmp" "$sums"; die "Aucun hash SHA-256 pour ${asset} dans SHA256SUMS — release incohérente."; }

  # 3. Télécharger le binaire correspondant.
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 \
    "${GITHUB_RELEASE}/${asset}" -o "$tmp" \
    || { rm -f "$tmp" "$sums"; die "Asset ${asset} introuvable dans la release."; }

  # 4. Vérification d'intégrité : refuse tout binaire altéré ou corrompu.
  actual="$(sha256sum "$tmp" | awk '{print $1}')"
  rm -f "$sums"
  [[ "$actual" == "$expected" ]] \
    || { rm -f "$tmp"; die "SHA-256 invalide pour ${asset} — installation annulée (fichier altéré ou corrompu)."; }

  # 5. Smoke test : le binaire doit réellement s'exécuter sur ce système.
  chmod 0755 "$tmp"
  "$tmp" --help >/dev/null 2>&1 \
    || { rm -f "$tmp"; die "Le binaire téléchargé ne s'exécute pas sur ce système."; }

  install -m 0755 "$tmp" "$dest"
  rm -f "$tmp"
}

fetch_public_key() {
  local url="${GITHUB_RELEASE}/license_pub.key"
  local tmp="${PUBKEY_PATH}.new"
  curl -fL --retry 3 --connect-timeout 10 --proto '=https' --tlsv1.2 "$url" -o "$tmp" \
    || { rm -f "$tmp"; die "Clé publique introuvable dans la release. Publiez d'abord une release LABOSURF PRO contenant license_pub.key."; }
  tr -d '[:space:]' < "$tmp" > "${tmp}.clean"
  mv "${tmp}.clean" "$tmp"
  # Une clé publique Ed25519 valide = exactement 64 caractères hexadécimaux.
  # (la page 404 de GitHub dépasse 64 octets : la taille seule ne suffit pas)
  [[ "$(wc -c < "$tmp")" -eq 64 && "$(cat "$tmp")" =~ ^[0-9a-fA-F]{64}$ ]] \
    || { rm -f "$tmp"; die "Clé publique de release absente ou invalide (64 caractères hex attendus)."; }
  install -m 0644 "$tmp" "$PUBKEY_PATH"
  rm -f "$tmp"
}

prepare_dirs() {
  install -d -m 0755 "$INSTALL_DIR" "$CONFIG_DIR" "${CONFIG_DIR}/engines"
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

# ── Sélection interactive des moteurs à installer ──────────
select_engines() {
  # ENV override : LABOSURF_ENGINES="xray,hysteria"
  local override="${LABOSURF_ENGINES:-}"
  if [[ -n "$override" ]]; then
    SELECTED_ENGINES="${override//,/ }"
    SELECTED_ENGINES="${SELECTED_ENGINES// /}"
    return
  fi

  info "Moteurs disponibles : $ENGINE_NAMES"
  echo
  printf '  Entrez les moteurs à installer (séparés par des espaces, "all" pour tous) : '
  IFS= read -r SELECTED_ENGINES < /dev/tty
}

# ── Déploiement d'un binaire moteur autonome ──────────────
install_engine_binary() {
  local eng="$1" dest="/usr/local/bin/labosurf-${eng}"
  download_asset "labosurf-${eng}" "$dest"
  ok "Binaire ${eng} installé"
}

# ── Service systemd par moteur (supervision du vrai binaire tierce) ──
install_engine_service() {
  local eng="$1" eng_service="/etc/systemd/system/labosurf-${eng}.service"
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

# Fichier de configuration d'environnement d'un moteur (source du binaire tierce).
gen_engine_conf() {
  local eng="$1" conf="/etc/labosurf/engines/${eng}.conf"
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
  if ! /usr/local/bin/labosurf-${eng} install; then
    warn "Moteur ${eng} : sources tierce non provisionnées (URL/SHA-256). `systemctl --no-pager status labosurf-${eng} 2>/dev/null | head -5 || true`"
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
  ok "Utilisateur système labosurf prêt (authorized_keys centralisés)"
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

# ── Point d'entrée ─────────────────────────────────────────
main() {
  require_root
  print_intro

  # ── [1/8] Système ──────────────────────────────────────
  step_begin 1 'Vérification du système'
  check_os
  step_ok "Système Linux détecté ($(source /etc/os-release && echo "${PRETTY_NAME:-$ID}"))"

  # ── [2/8] Dépendances ──────────────────────────────────
  step_begin 2 'Préparation des dépendances'
  run_step 'Installation des composants système...' install_deps

  # ── [3/8] Réseau ───────────────────────────────────────
  step_begin 3 'Configuration réseau (TUN, NAT, forwarding)'
  run_step 'Activation du tunnel et des règles réseau...' setup_network

  # ── [4/8] Répertoires ──────────────────────────────────
  step_begin 4 'Préparation des répertoires'
  run_step "Création de l'environnement LABOSURF PRO..." prepare_dirs

  # ── [5/9] Binaire gestionnaire ─────────────────────────
  step_begin 5 'Déploiement du binaire gestionnaire'
  run_step "Téléchargement du gestionnaire..." download_asset "labosurf" "$BIN_PATH"
  step_ok "Gestionnaire installé pour $(uname -m)"

  # ── [6/9] Moteurs ──────────────────────────────────────
  step_begin 6 'Installation des moteurs'
  select_engines
  info "Moteurs sélectionnés : ${SELECTED_ENGINES}"
  for eng in $SELECTED_ENGINES; do
    case " $ENGINE_NAMES " in
      *" $eng "*) ;;
      *) warn "Moteur inconnu ignoré : $eng"; continue ;;
    esac
    run_step "Déploiement du binaire moteur ${eng}..." install_engine_binary "$eng"
    run_step "Installation du service systemd ${eng}..." install_engine_service "$eng"
    run_step "Génération de la configuration ${eng}..." gen_engine_conf "$eng"
    if [[ "$eng" == "ssh" ]]; then
      run_step "Provisionnement de l'utilisateur SSH..." install_ssh_user
    fi
    systemctl daemon-reload
    systemctl enable "labosurf-${eng}.service" >/dev/null
    install_engine_thirdparty "$eng"
    systemctl restart "labosurf-${eng}.service"
    step_ok "Moteur ${eng} installé"
  done

  # ── [7/9] Licence ──────────────────────────────────────
  step_begin 7 'Sécurisation de la licence'
  run_step 'Installation de la clé publique...' fetch_public_key
  step_ok 'Clé publique installée'
  activate_license
  step_ok 'Licence activée'

  # ── [8/9] Service ──────────────────────────────────────
  step_begin 8 'Configuration du service'
  run_step 'Installation du service systemd...' install_service
  run_step 'Installation de la commande menu...' install_menu_command
  step_ok 'Service systemd configuré et démarré'

  # ── [9/9] Validation ───────────────────────────────────
  step_begin 9 'Validation finale'
  run_step "Vérification de l'installation..." final_check

  print_done
}

main "$@"
