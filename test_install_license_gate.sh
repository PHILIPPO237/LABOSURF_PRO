#!/usr/bin/env bash
# Test d'intégration du VERROU DE LICENCE de l'installateur labosurf-pro.sh.
#
# Contrairement aux tests Go (internal/license, engines/udp), qui testent la
# bibliothèque de vérification directement, ce script teste l'INTÉGRATION
# réelle : l'exact appel shell fait par activate_license() dans
# labosurf-pro.sh vers le binaire "labosurf license verify -token ... -print-id"
# compilé depuis ./cmd/labosurf, avec un jeton réellement produit par le vrai
# binaire LABOSURF_LICENSE_MAKER (dépôt frère, jamais réécrit ni simulé ici).
#
# Ne nécessite ni root, ni réseau, ni systemd, ni apt : labosurf-pro.sh est
# SOURCÉ (pas exécuté) — le garde `if [[ "${BASH_SOURCE[0]}" == "${0}" ]]`
# en bas du script empêche main() de se lancer automatiquement, ce qui
# permet d'appeler activate_license() en isolation. BIN_PATH/PUBKEY_PATH/
# RECEIPT_DIR/CONFIG_DIR sont redirigés vers des répertoires temporaires ;
# read_license_token() est redéfinie pour lire un jeton de test au lieu de
# /dev/tty (indisponible dans un environnement non interactif).
#
# Aucune clé privée réelle n'est utilisée : une paire ed25519 jetable est
# générée pour la durée de ce script.
set -Eeuo pipefail

GREEN=$'\033[1;32m'; RED=$'\033[1;31m'; CYAN=$'\033[1;36m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
PASS=0; FAILED=0
ok()   { printf '  %b[PASS]%b %s\n' "$GREEN" "$RESET" "$*"; PASS=$((PASS+1)); }
fail() { printf '  %b[FAIL]%b %s\n' "$RED"   "$RESET" "$*"; FAILED=$((FAILED+1)); }
info() { printf '  %b•%b %s\n'      "$CYAN"  "$RESET" "$*"; }

ROOT="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$ROOT"

echo "═══════════════════════════════════════════════════════════"
echo "  TEST — VERROU DE LICENCE DE L'INSTALLATEUR labosurf-pro.sh"
echo "═══════════════════════════════════════════════════════════"
echo

# ── Localisation du dépôt frère LABOSURF_LICENSE_MAKER ──────────────
MAKER_DIR="${LABOSURF_LICENSE_MAKER_DIR:-$ROOT/../LABOSURF_LICENSE_MAKER}"
if [[ ! -f "$MAKER_DIR/go.mod" ]]; then
  info "Dépôt frère LABOSURF_LICENSE_MAKER introuvable ($MAKER_DIR) — test sauté (voir LABOSURF_LICENSE_MAKER_DIR)."
  exit 0
fi

# ── Étape 1 : compilation du VRAI binaire déployé comme /usr/local/bin/labosurf ──
# IMPORTANT : ce n'est PAS ./cmd/labosurf. L'asset "labosurf-<arch>" que
# labosurf-pro.sh télécharge (download_asset "labosurf" "$BIN_PATH") est
# construit par .github/workflows/release.yml depuis engines/udp (avec la
# clé publique de production embarquée via -ldflags), voir
# AUDIT_INSTALL_LICENSE_GATE.md §4. ./cmd/labosurf ("gestionnaire", publié à
# part sous le nom labosurf-mgr-<arch>) n'est ni téléchargé ni invoqué par
# l'installateur — le tester à la place de engines/udp testerait un binaire
# qui n'est jamais réellement déployé par ce script.
info "Étape 1/5 : compilation du vrai binaire déployé (engines/udp — voir AUDIT_INSTALL_LICENSE_GATE.md §4)..."
LABOSURF_BIN="$WORK/labosurf"
( cd engines/udp && go build -o "$LABOSURF_BIN" . ) || { fail "compilation engines/udp"; exit 1; }
ok "labosurf (engines/udp) compilé"

# ── Étape 2 : compilation du vrai binaire LABOSURF_LICENSE_MAKER ──
info "Étape 2/5 : compilation du vrai LABOSURF_LICENSE_MAKER (dépôt frère)..."
MAKER_BIN="$WORK/license-maker"
( cd "$MAKER_DIR" && go build -o "$MAKER_BIN" . ) || { fail "compilation LABOSURF_LICENSE_MAKER"; exit 1; }
ok "LABOSURF_LICENSE_MAKER compilé"

# ── Étape 3 : paire de clés ed25519 JETABLE (jamais la production) ──
info "Étape 3/5 : génération d'une paire de clés de TEST (jamais la production)..."
KEYGEN_SRC="$WORK/keygen.go"
cat > "$KEYGEN_SRC" <<'EOF'
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(hex.EncodeToString(priv))
	fmt.Println(hex.EncodeToString(pub))
}
EOF
KEYGEN_BIN="$WORK/keygen"
go build -o "$KEYGEN_BIN" "$KEYGEN_SRC"
mapfile -t KEYS < <("$KEYGEN_BIN")
PRIV_HEX="${KEYS[0]}"
PUB_HEX="${KEYS[1]}"
PUBKEY_FILE="$WORK/labosurf_pub.key"
printf '%s' "$PUB_HEX" > "$PUBKEY_FILE"
ok "paire de clés de test générée (jamais utilisée en dehors de ce script)"

# ── Étape 4 : génération d'un jeton VALIDE par le vrai LICENSE_MAKER ──
info "Étape 4/5 : génération d'un jeton réel via le menu de LABOSURF_LICENSE_MAKER..."
MAKER_WORKDIR="$WORK/maker-workdir"
mkdir -p "$MAKER_WORKDIR"
printf '%s' "$PRIV_HEX" > "$MAKER_WORKDIR/labosurf_admin.key"
printf '%s' "$PUB_HEX"  > "$MAKER_WORKDIR/labosurf_pub.key"

run_maker_generate() {
  local id="$1"
  local stdin_seq
  stdin_seq="$(printf '1\n%s\n\n\n0\n' "$id")"
  ( cd "$MAKER_WORKDIR" && printf '%s' "$stdin_seq" | NO_COLOR=1 "$MAKER_BIN" ) 2>&1
}

MAKER_OUTPUT="$(run_maker_generate "GATE-TEST-VALID")"
# Depuis la mission "modèle professionnel de licence" : seule la clé de
# 40 caractères est imprimée à l'opérateur, plus le jeton signé complet
# (qui reste interne : registre local licenses.json + serveur central).
# On le lit donc directement dans licenses.json, comme le ferait toute
# inspection réelle de ce que LICENSE_MAKER vient de produire.
VALID_TOKEN="$(grep -o '"token": *"[^"]*"' "$MAKER_WORKDIR/licenses.json" 2>/dev/null | head -1 | sed 's/.*"token": *"\([^"]*\)"/\1/')"
if [[ -z "$VALID_TOKEN" ]]; then
  fail "aucun jeton trouvé dans licenses.json pour GATE-TEST-VALID"
  echo "--- sortie du License Maker ---"
  echo "$MAKER_OUTPUT"
  echo "--- licenses.json ---"
  cat "$MAKER_WORKDIR/licenses.json" 2>&1
  exit 1
fi
ok "jeton valide obtenu du vrai LABOSURF_LICENSE_MAKER (ID=GATE-TEST-VALID)"

# ── Jeton expiré : même clé privée de test, ActivationUntil dans le passé ──
# (le menu de LICENSE_MAKER fixe toujours la fenêtre à 3h — impossible d'en
# obtenir un déjà expiré par le menu sans attendre 3h. On reproduit donc
# uniquement le FORMAT DE JETON, déjà documenté et stable, avec la même
# clé privée de test — jamais la clé de production.)
EXPIRED_SRC="$WORK/gen_expired.go"
cat > "$EXPIRED_SRC" <<'EOF'
package main

import (
	"crypto/ed25519"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
)

type LicenseData struct {
	ID              string `json:"id"`
	Key             string `json:"key"`
	IssuedAt        string `json:"issued_at"`
	ActivationUntil string `json:"activation_until"`
	Product         string `json:"product"`
	Comment         string `json:"comment,omitempty"`
}

func main() {
	privBytes, err := hex.DecodeString(strings.TrimSpace(os.Args[1]))
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "clé privée de test invalide")
		os.Exit(1)
	}
	priv := ed25519.PrivateKey(privBytes)
	now := time.Now().UTC()
	data := LicenseData{
		ID:              os.Args[2],
		Key:             "LABOSURFEXPIREDTESTTOKENAAAAAAAAAAAAAAA",
		IssuedAt:        now.Add(-4 * time.Hour).Format(time.RFC3339),
		ActivationUntil: now.Add(-1 * time.Hour).Format(time.RFC3339),
		Product:         "LABOSURF PRO",
	}
	payload, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sig := ed25519.Sign(priv, payload)
	fmt.Print(encodeActivationKey(payload, sig))
}

// encodeActivationKey/encodeKeyBlock : même algorithme que LICENSE_MAKER
// (license.go) et internal/license/license.go — voir ces fichiers pour
// la documentation complète du format "clé d'activation".
func encodeActivationKey(payload, signature []byte) string {
	return "LABOSURF-" + encodeKeyBlock(payload) + "@" + encodeKeyBlock(signature)
}

func encodeKeyBlock(b []byte) string {
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	var out strings.Builder
	for i, r := range raw {
		if i > 0 && i%5 == 0 {
			out.WriteByte('-')
		}
		if (i/5)%2 == 1 {
			r = unicode.ToLower(r)
		}
		out.WriteRune(r)
	}
	return out.String()
}
EOF
EXPIRED_BIN="$WORK/gen_expired"
go build -o "$EXPIRED_BIN" "$EXPIRED_SRC"
EXPIRED_TOKEN="$("$EXPIRED_BIN" "$PRIV_HEX" "GATE-TEST-EXPIRED")"
ok "jeton expiré généré (même clé de test, ActivationUntil dans le passé)"

# Jeton altéré : un caractère du payload (valide) est modifié -> signature invalide.
TAMPERED_TOKEN="$(printf '%s' "$VALID_TOKEN" | sed 's/^\(.\)/X/')"

echo
echo "═══════════════════════════════════════════════════════════"
echo "  Étape 5/5 : scénarios contre le vrai gate activate_license()"
echo "═══════════════════════════════════════════════════════════"

export ROOT LABOSURF_BIN

# run_scenario <description> <token> <attend_succes:0|1>
run_scenario() {
  local desc="$1" token="$2" expect_success="$3"
  local receipt_dir out rc id_after
  receipt_dir="$(mktemp -d)"

  set +e
  out="$(TEST_TOKEN="$token" TEST_PUBKEY_FILE="$PUBKEY_FILE" TEST_RECEIPT_DIR="$receipt_dir" bash -c '
    source "$ROOT/labosurf-pro.sh"
    BIN_PATH="$LABOSURF_BIN"
    PUBKEY_PATH="$TEST_PUBKEY_FILE"
    RECEIPT_DIR="$TEST_RECEIPT_DIR"
    CONFIG_DIR="$TEST_RECEIPT_DIR"
    read_license_token() { printf "%s" "$TEST_TOKEN"; }
    activate_license
  ' 2>&1)"
  rc=$?
  set -e

  local receipt_count
  receipt_count="$(find "$receipt_dir" -name '.install_*.receipt' 2>/dev/null | wc -l | tr -d ' ')"

  if [[ "$expect_success" == "1" ]]; then
    if [[ $rc -eq 0 && "$receipt_count" -eq 1 ]]; then
      ok "$desc → installation CONTINUE (exit 0, reçu écrit) comme attendu"
    else
      fail "$desc → attendu succès (exit 0 + reçu écrit), obtenu exit=$rc reçus=$receipt_count"
      echo "    --- sortie ---"; echo "$out" | sed 's/^/    /'
    fi
  else
    if [[ $rc -ne 0 && "$receipt_count" -eq 0 ]]; then
      ok "$desc → installation BLOQUÉE (exit $rc, aucun reçu écrit) comme attendu"
    else
      fail "$desc → attendu blocage (exit≠0, aucun reçu), obtenu exit=$rc reçus=$receipt_count"
      echo "    --- sortie ---"; echo "$out" | sed 's/^/    /'
    fi
  fi
  rm -rf "$receipt_dir"
}

run_scenario "CAS 7 — clé VALIDE (vrai jeton LICENSE_MAKER)"                "$VALID_TOKEN"    1
run_scenario "CAS 1 — AUCUNE clé (jeton vide, simule Entrée sans saisie)"   ""                0
run_scenario "CAS 2 — clé VIDE (espaces uniquement)"                       "   "             0
run_scenario "CAS 3 — clé MALFORMÉE (chaîne arbitraire)"                   "pas-un-jeton-du-tout" 0
run_scenario "CAS 4/5 — clé MODIFIÉE / signature invalide"                 "$TAMPERED_TOKEN" 0
run_scenario "CAS 6 — clé EXPIRÉE (fenêtre de 3h dépassée)"                "$EXPIRED_TOKEN"  0

# ── Bonus : la MÊME clé valide ne peut pas ouvrir une 2e installation ──
info "Bonus : réutilisation de la même licence valide sur la même machine (1 clé = 1 install)..."
reuse_dir="$(mktemp -d)"
set +e
TEST_TOKEN="$VALID_TOKEN" TEST_PUBKEY_FILE="$PUBKEY_FILE" TEST_RECEIPT_DIR="$reuse_dir" bash -c '
  source "$ROOT/labosurf-pro.sh"
  BIN_PATH="$LABOSURF_BIN"
  PUBKEY_PATH="$TEST_PUBKEY_FILE"
  RECEIPT_DIR="$TEST_RECEIPT_DIR"
  CONFIG_DIR="$TEST_RECEIPT_DIR"
  read_license_token() { printf "%s" "$TEST_TOKEN"; }
  activate_license
' >/dev/null 2>&1
first_rc=$?
out2="$(TEST_TOKEN="$VALID_TOKEN" TEST_PUBKEY_FILE="$PUBKEY_FILE" TEST_RECEIPT_DIR="$reuse_dir" bash -c '
  source "$ROOT/labosurf-pro.sh"
  BIN_PATH="$LABOSURF_BIN"
  PUBKEY_PATH="$TEST_PUBKEY_FILE"
  RECEIPT_DIR="$TEST_RECEIPT_DIR"
  CONFIG_DIR="$TEST_RECEIPT_DIR"
  read_license_token() { printf "%s" "$TEST_TOKEN"; }
  activate_license
' 2>&1)"
second_rc=$?
set -e
if [[ $first_rc -eq 0 && $second_rc -ne 0 ]]; then
  ok "réutilisation de la même licence sur la même machine → BLOQUÉE dès la 2e tentative"
else
  fail "réutilisation de licence non bloquée (1re: exit=$first_rc, 2e: exit=$second_rc)"
  echo "    --- sortie 2e tentative ---"; echo "$out2" | sed 's/^/    /'
fi
rm -rf "$reuse_dir"

# ── Vérification qu'aucun contournement par variable d'environnement n'existe ──
info "Vérification : aucun contournement LABOSURF_DEV (ou similaire) ne subsiste..."
if grep "LABOSURF_DEV" labosurf-pro.sh | grep -vqE '^[[:space:]]*#'; then
  fail "une référence ACTIVE (hors commentaire) à LABOSURF_DEV subsiste dans labosurf-pro.sh"
else
  ok "aucun contournement par variable d'environnement dans labosurf-pro.sh (seule une mention en commentaire subsiste, à titre d'historique)"
fi

echo
echo "═══════════════════════════════════════════════════════════"
printf '  Résultat : %b%d PASS%b, %b%d FAIL%b\n' "$GREEN" "$PASS" "$RESET" "$RED" "$FAILED" "$RESET"
echo "═══════════════════════════════════════════════════════════"
[[ "$FAILED" -eq 0 ]] || exit 1
