#!/usr/bin/env bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
go test ./internal/license/ -v 2>&1 | grep -E "^(--- PASS|--- FAIL|PASS|FAIL|ok)" | head -30
