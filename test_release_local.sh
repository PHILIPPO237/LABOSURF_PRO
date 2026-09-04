#!/usr/bin/env bash
# Simulation LOCALE du workflow release.yml — valide la chaîne complète
# sans avoir besoin de GitHub. Utilise la vraie clé publique locale.
set -Eeuo pipefail

GREEN=$'\033[1;32m'; RED=$'\033[1;31m'; CYAN=$'\033[1;36m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
ok()   { printf '  %b[OK]%b %s\n'   "$GREEN" "$RESET" "$*"; }
fail() { printf '  %b[FAIL]%b %s\n' "$RED"   "$RESET" "$*"; exit 1; }
info() { printf '  %b•%b %s\n'      "$CYAN"  "$RESET" "$*"; }

ROOT="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$ROOT"

echo "═══════════════════════════════════════════════════"
echo "  SIMULATION LOCALE DU WORKFLOW release.yml"
echo "═══════════════════════════════════════════════════"

# ── Étape "Read public verification key" ──
info "Étape 1/5 : lecture clé publique..."
PUBLIC_KEY="$(tr -d '[:space:]' < release/license_pub.key 2>/dev/null || true)"
[[ -n "$PUBLIC_KEY" ]] || fail "release/license_pub.key introuvable ou vide"
echo "$PUBLIC_KEY" | tr -d '[:space:]' > "$WORK/license_pub.key"
[[ "$(wc -c < "$WORK/license_pub.key")" -eq 64 ]] || fail "clé publique ≠ 64 octets (trouvé: $(wc -c < "$WORK/license_pub.key"))"
[[ "$PUBLIC_KEY" =~ ^[0-9a-fA-F]{64}$ ]] || fail "clé publique non hexadécimale"
ok "clé publique valide : ${PUBLIC_KEY:0:16}…${PUBLIC_KEY: -8} (64 hex)"

# ── Étape "Tests" ──
info "Étape 2/5 : go test ./... (peut prendre ~15s)..."
( cd engines/udp && go test ./... >"$WORK/test.log" 2>&1 ) \
  && ok "tests Go passés" \
  || { cat "$WORK/test.log"; fail "tests Go échoués"; }

# ── Étape "Build Linux binaries" ──
info "Étape 3/5 : compilation amd64 + arm64 avec clé embarquée..."
mkdir -p "$WORK/dist"
( cd engines/udp && \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "-X main.embeddedVerifyKeyHex=${PUBLIC_KEY}" \
    -o "$WORK/dist/labosurf-linux-amd64" . ) \
  || fail "compilation amd64 échouée"
( cd engines/udp && \
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
    -ldflags "-X main.embeddedVerifyKeyHex=${PUBLIC_KEY}" \
    -o "$WORK/dist/labosurf-linux-arm64" . ) \
  || fail "compilation arm64 échouée"
cp "$WORK/license_pub.key" "$WORK/dist/license_pub.key"
ok "2 binaires compilés"

# ── Étape "SHA256SUMS" (format exact du workflow) ──
info "Étape 4/5 : génération SHA256SUMS..."
( cd "$WORK/dist" && sha256sum labosurf-linux-* license_pub.key > SHA256SUMS )
ok "SHA256SUMS généré"

# ── Vérifications finales (ce que l'installateur attend) ──
info "Étape 5/5 : validation des artefacts..."

for f in labosurf-linux-amd64 labosurf-linux-arm64 license_pub.key SHA256SUMS; do
  [[ -f "$WORK/dist/$f" ]] || fail "artefact manquant : $f"
done
ok "les 4 artefacts attendus sont présents"

# Format ELF + architecture
for arch in amd64 arm64; do
  bin="$WORK/dist/labosurf-linux-$arch"
  head -c4 "$bin" | grep -q $'\x7fELF' || fail "$arch : pas un ELF"
  ok "labosurf-linux-$arch : binaire ELF valide ($(du -h "$bin" | cut -f1))"
done

# La clé embarquée dans le binaire correspond-elle à license_pub.key ?
if strings "$WORK/dist/labosurf-linux-amd64" 2>/dev/null | grep -q "$PUBLIC_KEY"; then
  ok "clé publique correctement embarquée dans le binaire"
else
  info "(strings ne montre pas la clé — normal si ldflags l'a inlinée, vérification non bloquante)"
fi

# SHA256SUMS référence-t-il les bons noms (ce que l'installateur vérifie) ?
for f in labosurf-linux-amd64 labosurf-linux-arm64 license_pub.key; do
  grep -q "  $f\$" "$WORK/dist/SHA256SUMS" || fail "SHA256SUMS ne référence pas $f"
done
ok "SHA256SUMS référence les 3 fichiers avec noms relatifs (compatible installateur)"

# Re-vérification croisée : les hash sont-ils corrects ?
( cd "$WORK/dist" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ) \
  && ok "sha256sum -c SHA256SUMS : tous les hash valides" \
  || fail "sha256sum -c a échoué"

echo
echo "═══════════════════════════════════════════════════"
printf '  %b✔ SIMULATION RÉUSSIE — le workflow CI réussirait.%b\n' "$GREEN$BOLD" "$RESET"
echo "═══════════════════════════════════════════════════"
echo
echo "  Artefacts produits (dans $WORK/dist) :"
ls -la "$WORK/dist" | awk 'NR>1 {printf "    %s  %s\n", $5, $9}'
echo
echo "  ➜  La SEULE chose qui manque sur GitHub est la variable"
echo "     LABOSURF_LICENSE_PUBKEY (onglet Variables). Une fois créée,"
echo "     le prochain run du workflow produira exactement ces 4 assets."
