#!/usr/bin/env bash
cd /mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO/engines/udp
go test -timeout 20m . > /tmp/udp_full.log 2>&1
echo "EXIT:$?" >> /tmp/udp_full.log
