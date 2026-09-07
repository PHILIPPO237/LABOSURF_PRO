#!/usr/bin/env bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO/engines/udp
go test -run 'TestLicense|TestIntegration|TestCross|TestInstall|TestClient|TestNoEmbedded|TestRunLicense|TestReadToken' -v . 2>&1 | grep -E "^(=== RUN|--- PASS|--- FAIL|PASS|FAIL|ok)" | grep -v "=== RUN.*/" | head -100
