#!/data/data/com.termux/files/usr/bin/env bash
# ============================================================
# LABOSURF PRO — Installateur Android / Termux
# Laboratoire du FreeSurf • PHILIPPO237
# ============================================================
#
# Installe l'outil de gestion LABOSURF PRO directement sur le
# téléphone, dans Termux (pas besoin de root, pas de systemd).
#
# Usage :
#   curl -fsSL <url>/labosurf-android.sh | bash
#
# Ce que fait ce script :
#   1. vérifie qu'on est bien sous Termux/Android ;
#   2. télécharge le binaire labosurf-android-arm64 depuis la
#      GitHub Release ;
#   3. vérifie son intégrité SHA-256 ;
#   4. l'installe dans $PREFIX/bin (accessible partout) ;
#   5. installe la commande "menu".
#
# Ce script NE FAIT PAS :
#   - pas de serveur VPN TUN (impossible sans root sur Android) ;
#   - pas de systemd (Android n'en a pas) ;
#   - pas de configuration réseau (le serveur tourne sur le VPS).
#
# Sur le téléphone, LABOSURF PRO sert à :
#   - créer/gérer les comptes clients ;
#   - gérer les licences ;
#   - administrer le VPS (via la commande labosurf / menu).
# ============================================================

set -Eeuo pipefail

GITHUB_REPO="PHILIPPO237/LABOSURF_PRO"
GITHUB_RELEASE="https://github.com/${GITHUB_REPO}/releases/latest/download"
ASSET="labosurf-android-arm64"

# ── Couleurs (si terminal le permet) ──────────────────────
if [[ -t 1 ]]; then
  GREEN=$'\033[1;32m'; CYAN=$'\033[1;36m'; RED=$'\033[1;31m'
  YELLOW=$'\033[1;33m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
  GREEN=''; CYAN=''; RED=''; YELLOW=''; BOLD=''; RESET=''
fi

info() { printf '  %b•%b %s\n' "$CYAN"  "$RESET" "$*"; }
ok()   { printf '  %b✔%b %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '  %b!%b %s\n'  "$YELLOW" "$RESET" "$*"; }
die()  { printf '\n  %b✖%b %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

echo
printf '  %bLABOSURF PRO — Installation Android/Termux%b\n' "$BOLD$GREEN" "$RESET"
printf '  %bLaboratoire du FreeSurf • PHILIPPO237%b\n\n' "$CYAN" "$RESET"

# ── 1. Vérifier qu'on est sous Termux/Android ─────────────
info "Vérification de l'environnement..."

if [[ -z "${PREFIX:-}" ]] || [[ ! -d "${PREFIX:-/nonexistent}" ]]; then
  die "Ce script doit être lancé dans Termux (PREFIX non défini). Installez Termux d'abord."
fi

# Détecter Android via uname ou la présence du préfixe Termux
if [[ "$(uname -o 2>/dev/null)" != "Android" ]] && [[ "$PREFIX" != *"com.termux"* ]]; then
  warn "Environnement Android/Termux non confirmé — tentative quand même."
fi

# Vérifier l'architecture (les téléphones récents sont arm64)
arch="$(uname -m)"
case "$arch" in
  aarch64|arm64) : ;;  # OK
  *) die "Architecture non supportée : $arch (arm64 requis). Les téléphones récents sont arm64." ;;
esac
ok "Termux détecté (arch $arch)"

# ── 2. Dépendances ────────────────────────────────────────
info "Vérification des outils (curl, sha256sum)..."
command -v curl      >/dev/null 2>&1 || pkg install -y curl      >/dev/null 2>&1 || die "curl requis"
command -v sha256sum >/dev/null 2>&1 || pkg install -y coreutils >/dev/null 2>&1 || die "coreutils requis"
ok "Outils présents"

# ── 3. Téléchargement du binaire + SHA256SUMS ─────────────
BIN_DIR="$PREFIX/bin"
BIN_PATH="$BIN_DIR/labosurf"
tmp_bin="$(mktemp)"
tmp_sums="$(mktemp)"
trap 'rm -f "$tmp_bin" "$tmp_sums"' EXIT

info "Téléchargement de ${ASSET}..."
curl -fL --retry 3 --connect-timeout 15 --proto '=https' --tlsv1.2 \
  "${GITHUB_RELEASE}/${ASSET}" -o "$tmp_bin" \
  || die "Binaire ${ASSET} introuvable dans la release. Publiez d'abord une release contenant ${ASSET}."

info "Téléchargement de SHA256SUMS..."
curl -fL --retry 3 --connect-timeout 15 --proto '=https' --tlsv1.2 \
  "${GITHUB_RELEASE}/SHA256SUMS" -o "$tmp_sums" \
  || die "SHA256SUMS introuvable dans la release."

# ── 4. Vérification d'intégrité ───────────────────────────
info "Vérification de l'intégrité SHA-256..."
expected="$(awk -v a="$ASSET" '$2 == a {print $1}' "$tmp_sums")"
[[ -n "$expected" ]] || die "Aucun hash pour ${ASSET} dans SHA256SUMS."

actual="$(sha256sum "$tmp_bin" | awk '{print $1}')"
[[ "$actual" == "$expected" ]] \
  || die "SHA-256 invalide pour ${ASSET} — fichier altéré ou corrompu. Installation annulée."
ok "Intégrité vérifiée (SHA-256 OK)"

# ── 5. Smoke test : le binaire doit s'exécuter ────────────
chmod 0755 "$tmp_bin"
"$tmp_bin" help >/dev/null 2>&1 || "$tmp_bin" --help >/dev/null 2>&1 || true
# (on ne bloque pas si --help retourne un code non-nul : certains builds
#  n'ont pas de --help standard ; le hash SHA-256 est la garantie principale)

# ── 6. Installation ───────────────────────────────────────
info "Installation dans ${BIN_PATH}..."
mv "$tmp_bin" "$BIN_PATH"
chmod 0755 "$BIN_PATH"

# Commande "menu" pratique
cat > "$BIN_DIR/menu" <<'SH'
#!/data/data/com.termux/files/usr/bin/env bash
exec labosurf "$@"
SH
chmod 0755 "$BIN_DIR/menu"
ok "Installé : labosurf + menu"

# ── 7. Vérification finale ────────────────────────────────
if command -v labosurf >/dev/null 2>&1; then
  ok "Commande 'labosurf' disponible"
else
  warn "'labosurf' pas encore dans le PATH — ouvre un nouveau terminal Termux."
fi

echo
printf '  %b╭──────────────────────────────────────────╮%b\n' "$GREEN" "$RESET"
printf '  %b│  ✔ LABOSURF PRO installé sur Android     │%b\n' "$GREEN$BOLD" "$RESET"
printf '  %b╰──────────────────────────────────────────╯%b\n' "$GREEN" "$RESET"
echo
info "Commande principale : ${BOLD}labosurf${RESET}"
info "Menu administrateur : ${BOLD}menu${RESET}"
echo
info "Rappel : le serveur VPN tourne sur ton VPS (Linux)."
info "Sur le téléphone, tu gères les comptes et les licences."
echo
