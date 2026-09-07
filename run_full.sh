#!/usr/bin/env bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO/engines/udp
echo "=== FULL UDP SUITE ==="
go test . 2>&1 | tail -5
echo "=== PLATFORM LICENSE ==="
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO
go test ./internal/license/ 2>&1 | tail -5
echo "=== BUILD ALL ==="
go build ./... 2>&1 | head -20
echo BUILD_DONE
