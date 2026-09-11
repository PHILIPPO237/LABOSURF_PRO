#!/usr/bin/env bash
cd "$(dirname "$0")"
go test -run 'TestLicense|TestIntegration|TestCross|TestInstall|TestClient|TestNoEmbedded|TestRunLicense|TestReadToken' -v . 2>&1 | grep -E "^(=== RUN|--- PASS|--- FAIL|PASS|FAIL|ok)" | grep -v "=== RUN.*/" | head -100
