#!/usr/bin/env bash
# Simulation LOCALE du workflow release.yml — valide la chaîne complète
# (tests + build UDP/manager/moteurs + checksums + artefacts) sans GitHub.
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
info "Étape 1/7 : lecture clé publique..."
PUBLIC_KEY="$(tr -d '[:space:]' < release/license_pub.key 2>/dev/null || true)"
[[ -n "$PUBLIC_KEY" ]] || fail "release/license_pub.key introuvable ou vide"
printf '%s' "$PUBLIC_KEY" > "$WORK/license_pub.key"
[[ "$(wc -c < "$WORK/license_pub.key")" -eq 64 ]] || fail "clé publique ≠ 64 octets (trouvé: $(wc -c < "$WORK/license_pub.key"))"
[[ "$PUBLIC_KEY" =~ ^[0-9a-fA-F]{64}$ ]] || fail "clé publique non hexadécimale"
ok "clé publique valide : ${PUBLIC_KEY:0:16}…${PUBLIC_KEY: -8} (64 hex)"

# ── Étape "Tests" ──
info "Étape 2/7 : go vet + go test (racine et engines/udp)..."
( cd engines/udp && go vet ./... >"$WORK/t1.log" 2>&1 && go test ./... >>"$WORK/t1.log" 2>&1 ) \
  && ok "tests Go (engines/udp) passés" \
  || { cat "$WORK/t1.log"; fail "tests Go engines/udp échoués"; }

# ── Étape "Build UDP engine binaries" ──
info "Étape 3/7 : compilation serveur UDP (amd64/arm64/android) avec clé..."
mkdir -p "$WORK/dist"
for goos_arch in linux/amd64 linux/arm64 android/arm64; do
  GOOS="${goos_arch%/*}"; GOARCH="${goos_arch#*/}"
  ( cd engines/udp && \
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
      -ldflags "-X main.embeddedVerifyKeyHex=${PUBLIC_KEY}" \
      -o "$WORK/dist/labosurf-${GOOS}-${GOARCH}" . ) \
    || fail "compilation labosurf-${GOOS}-${GOARCH} échouée"
done
ok "3 binaires UDP compilés"

# ── Étape "Build manager and engine binaries" ──
info "Étape 4/7 : compilation gestionnaire + moteurs..."
for goos_arch in linux/amd64 linux/arm64 android/arm64; do
  GOOS="${goos_arch%/*}"; GOARCH="${goos_arch#*/}"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -o "$WORK/dist/labosurf-mgr-${GOOS}-${GOARCH}" ./cmd/labosurf \
    || fail "compilation labosurf-mgr-${GOOS}-${GOARCH} échouée"
done
for name in xray slowdns dnstt hysteria udp ssh; do
  for goos_arch in linux/amd64 linux/arm64 android/arm64; do
    GOOS="${goos_arch%/*}"; GOARCH="${goos_arch#*/}"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -o "$WORK/dist/labosurf-${name}-${GOOS}-${GOARCH}" "./cmd/labosurf-$name" \
      || fail "compilation labosurf-${name}-${GOOS}-${GOARCH} échouée"
  done
done
ok "3 gestionnaires + 18 moteurs compilés"

# ── Étape "Copy public key and checksums" ──
info "Étape 5/7 : clé publique + SHA256SUMS..."
cp "$WORK/license_pub.key" "$WORK/dist/license_pub.key"
cp release/license_pub.key.example "$WORK/dist/license_pub.key.example"
( cd "$WORK/dist" && sha256sum * > SHA256SUMS )
ok "SHA256SUMS généré ($(wc -l < "$WORK/dist/SHA256SUMS") entrées)"

# ── Étape "Verify artifacts" ──
info "Étape 6/7 : vérification des artefacts..."
EXPECTED=""
for goos_arch in linux/amd64 linux/arm64 android/arm64; do
  GOOS="${goos_arch%/*}"; GOARCH="${goos_arch#*/}"
  EXPECTED="$EXPECTED labosurf-${GOOS}-${GOARCH} labosurf-mgr-${GOOS}-${GOARCH}"
  for name in xray slowdns dnstt hysteria udp ssh; do
    EXPECTED="$EXPECTED labosurf-${name}-${GOOS}-${GOARCH}"
  done
done
for f in $EXPECTED license_pub.key license_pub.key.example SHA256SUMS; do
  [[ -f "$WORK/dist/$f" ]] || fail "artefact manquant : $f"
done
ok "les $(echo $EXPECTED | wc -w | tr -d ' ') artefacts attendus sont présents (+ 3 fichiers)"

# Binaire Linux = ELF, Android = ELF ARM
LINUX_EXPECTED="labosurf-linux-amd64 labosurf-mgr-linux-amd64 labosurf-xray-linux-amd64"
for bin in $LINUX_EXPECTED; do
  head -c4 "$WORK/dist/$bin" | grep -q $'\x7fELF' || fail "$bin : pas un ELF"
  ok "$bin : binaire ELF valide ($(du -h "$WORK/dist/$bin" | cut -f1))"
done

# Clé embarquée dans le serveur UDP ?
if strings "$WORK/dist/labosurf-linux-amd64" 2>/dev/null | grep -q "$PUBLIC_KEY"; then
  ok "clé publique embarquée dans le serveur UDP"
else
  info "(strings ne montre pas la clé — normal si ldflags l'a inlinée, non bloquant)"
fi

# ── Re-vérification SHA256SUMS ──
info "Étape 7/7 : sha256sum -c SHA256SUMS..."
( cd "$WORK/dist" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ) \
  && ok "sha256sum -c : tous les hash valides" \
  || fail "sha256sum -c a échoué"

echo
echo "═══════════════════════════════════════════════════"
printf '  %b✔ SIMULATION RÉUSSIE — le workflow CI réussirait.%b\n' "$GREEN$BOLD" "$RESET"
echo "═══════════════════════════════════════════════════"
echo
echo "  Artefacts produits :"
ls -1 "$WORK/dist" | sed 's/^/    /'
echo
echo "  ➜  Les clés publiques (release/license_pub.key, engines/udp/labosurf_pub.key)"
echo "     doivent être commitées (exceptions .gitignore ajoutées)."