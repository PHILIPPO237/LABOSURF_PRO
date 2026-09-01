#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

command -v git >/dev/null 2>&1 || { echo "ERREUR: git n'est pas installé." >&2; exit 1; }
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "ERREUR: dépôt Git introuvable." >&2; exit 1; }

echo "=== LABOSURF PRO — PUBLICATION GITHUB ==="
echo "Racine : $ROOT"

git diff --check
if command -v go >/dev/null 2>&1 && [[ -f engines/udp/go.mod ]]; then
  (cd engines/udp && go test ./...)
else
  echo "AVERTISSEMENT : Go absent localement ; les tests seront exécutés par GitHub Actions."
fi

if [[ -z "$(git status --porcelain)" ]]; then
  echo "Aucune modification locale à publier."
  exit 0
fi

MSG="${1:-LABOSURF PRO: mise à jour}"
git add -A
git commit -m "$MSG"
git push origin HEAD

echo "Publication terminée."
