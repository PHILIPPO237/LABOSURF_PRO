#!/usr/bin/env bash
# Tests de robustesse de l'expérience terminal de labosurf-pro.sh
# (bannière, couleurs, fallback Unicode/ASCII, erreurs réseau, permissions,
# terminal non interactif). Complète test_install_license_gate.sh, qui se
# concentre uniquement sur le verrou cryptographique de licence.
#
# Ne nécessite ni root réel, ni réseau réel, ni systemd : chaque scénario
# source labosurf-pro.sh (le garde BASH_SOURCE empêche main() de se lancer)
# et invoque directement les fonctions concernées, avec permissions/tty/
# réseau simulés.
set -Eeuo pipefail

GREEN=$'\033[1;32m'; RED=$'\033[1;31m'; CYAN=$'\033[1;36m'; RESET=$'\033[0m'
PASS=0; FAILED=0
ok()   { printf '  %b[PASS]%b %s\n' "$GREEN" "$RESET" "$*"; PASS=$((PASS+1)); }
fail() { printf '  %b[FAIL]%b %s\n' "$RED"   "$RESET" "$*"; FAILED=$((FAILED+1)); }
info() { printf '  %b•%b %s\n'      "$CYAN"  "$RESET" "$*"; }

ROOT="$(cd "$(dirname "$0")" && pwd)"
export ROOT
cd "$ROOT"

echo "═══════════════════════════════════════════════════════════"
echo "  TEST — ROBUSTESSE UX DE L'INSTALLATEUR labosurf-pro.sh"
echo "═══════════════════════════════════════════════════════════"
echo

# ── 1. Détection Unicode : locale non-UTF-8 → glyphes ASCII ──
out="$(LC_ALL=C LANG=C bash -c 'source "$ROOT/labosurf-pro.sh"; printf "%s|%s|%s" "$UNICODE_OK" "$ICON_OK" "$BOX_TL"')"
if [[ "$out" == "0|v|+" ]]; then
  ok "Locale non-UTF-8 (LC_ALL=C) → fallback ASCII (UNICODE_OK=0, icônes/bordures ASCII)"
else
  fail "Fallback ASCII incorrect : obtenu '$out'"
fi

# ── 2. Détection Unicode : locale UTF-8 → glyphes Unicode ──
set +e
out="$(LC_ALL=C.UTF-8 LANG=C.UTF-8 bash -c 'source "$ROOT/labosurf-pro.sh"; printf "%s" "$UNICODE_OK"' 2>/dev/null)"
set -e
if [[ "$out" == "1" ]]; then
  ok "Locale UTF-8 (LC_ALL=C.UTF-8) → rendu Unicode activé (UNICODE_OK=1)"
else
  info "Locale C.UTF-8 indisponible dans cet environnement (obtenu UNICODE_OK='$out') — vérifié via \$LANG à la place"
  out2="$(LC_ALL= LANG=en_US.UTF-8 bash -c 'source "$ROOT/labosurf-pro.sh"; printf "%s" "$UNICODE_OK"')"
  if [[ "$out2" == "1" ]]; then
    ok "LANG=en_US.UTF-8 → rendu Unicode activé (UNICODE_OK=1)"
  else
    fail "Détection Unicode ne s'active jamais, même avec LANG=en_US.UTF-8 (obtenu '$out2')"
  fi
fi

# ── 3. Couleurs désactivées quand stdout n'est pas un terminal ──
out="$(bash -c 'source "$ROOT/labosurf-pro.sh"; printf "[%s]" "$RESET"' < /dev/null | cat)"
if [[ "$out" == "[]" ]]; then
  ok "stdout non-tty (pipé) → couleurs désactivées (\$RESET vide)"
else
  fail "Couleurs non désactivées sur stdout non-tty : obtenu '$out'"
fi

# ── 4. require_root bloque proprement un utilisateur non-root ──
set +e
out="$(bash -c '
  source "$ROOT/labosurf-pro.sh"
  id() { printf "1000\n"; }   # simule un utilisateur non-root
  require_root
' 2>&1)"
rc=$?
set -e
if [[ $rc -ne 0 ]] && printf '%s' "$out" | grep -qi "sudo/root"; then
  ok "require_root refuse un utilisateur non-root (exit≠0, message clair)"
else
  fail "require_root n'a pas bloqué correctement (exit=$rc) : $out"
fi

# ── 5. Erreur réseau pendant le téléchargement → échec propre, pas de faux succès ──
tmp_dest="$(mktemp -u)"
set +e
out="$(GITHUB_RELEASE="https://example.invalid/does-not-exist" bash -c '
  source "$ROOT/labosurf-pro.sh"
  download_asset "labosurf" "'"$tmp_dest"'"
' 2>&1)"
rc=$?
set -e
if [[ $rc -ne 0 ]] && [[ ! -f "$tmp_dest" ]] && printf '%s' "$out" | grep -q "Installation failed"; then
  ok "Échec réseau pendant le téléchargement → 'Installation failed' affiché, aucun fichier créé, exit≠0"
else
  fail "Échec réseau mal géré (exit=$rc, fichier présent=$([[ -f "$tmp_dest" ]] && echo oui || echo non))"
  echo "$out" | sed 's/^/    /'
fi
rm -f "$tmp_dest"

# ── 6. Terminal non interactif (aucun /dev/tty) → activate_license échoue fermé, jamais ouvert ──
tty_available=1
if ! ( exec 3</dev/tty ) 2>/dev/null; then
  tty_available=0
fi
if [[ "$tty_available" -eq 0 ]]; then
  pubkey_tmp="$(mktemp)"; receipt_tmp="$(mktemp -d)"
  set +e
  out="$(TEST_PUBKEY_FILE="$pubkey_tmp" TEST_RECEIPT_DIR="$receipt_tmp" bash -c '
    source "$ROOT/labosurf-pro.sh"
    BIN_PATH="/bin/true"
    PUBKEY_PATH="$TEST_PUBKEY_FILE"
    RECEIPT_DIR="$TEST_RECEIPT_DIR"
    activate_license
  ' 2>&1)"
  rc=$?
  set -e
  rm -rf "$pubkey_tmp" "$receipt_tmp"
  if [[ $rc -ne 0 ]] && printf '%s' "$out" | grep -qi "No activation key provided"; then
    ok "Aucun /dev/tty disponible (environnement non interactif) → échec FERMÉ (bloqué), jamais ouvert"
  else
    fail "Comportement incorrect sans /dev/tty (exit=$rc) :"
    echo "$out" | sed 's/^/    /'
  fi
else
  info "/dev/tty disponible dans cet environnement — scénario non applicable ici (déjà couvert par lecture directe du code)"
fi

# ── 7. Message d'erreur structuré (die()) : étape + raison, sans secret ──
set +e
out="$(bash -c '
  source "$ROOT/labosurf-pro.sh"
  step_begin 4 "Dependencies"
  die "simulated failure reason"
' 2>&1)"
set -e
if printf '%s' "$out" | grep -q "Installation failed" \
   && printf '%s' "$out" | grep -q "Step   : \[4/11\] Dependencies" \
   && printf '%s' "$out" | grep -q "Reason : simulated failure reason"; then
  ok "die() affiche un bloc structuré (Installation failed / Step / Reason)"
else
  fail "Format d'erreur structuré incorrect :"
  echo "$out" | sed 's/^/    /'
fi

# ── 8. Étapes annoncées = 11, correspondant à la liste de la mission ──
out="$(bash -c 'source "$ROOT/labosurf-pro.sh"; printf "%s\n" "${STEP_LABELS[@]}"')"
expected=$'System Check\nLicense Validation\nEnvironment Preparation\nDependencies\nDownload Components\nInstall Binaries\nConfigure LABOSURF PRO\nInstall Services\nStart Services\nHealth Check\nFinalization'
if [[ "$out" == "$expected" ]]; then
  ok "Les 11 étapes annoncées correspondent exactement à la liste demandée"
else
  fail "Liste d'étapes différente de celle attendue"
  diff <(printf '%s' "$expected") <(printf '%s' "$out") | sed 's/^/    /' || true
fi

echo
echo "═══════════════════════════════════════════════════════════"
printf '  Résultat : %b%d PASS%b, %b%d FAIL%b\n' "$GREEN" "$PASS" "$RESET" "$RED" "$FAILED" "$RESET"
echo "═══════════════════════════════════════════════════════════"
[[ "$FAILED" -eq 0 ]] || exit 1
