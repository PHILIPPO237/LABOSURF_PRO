#!/bin/bash
curl -sL 'https://api.github.com/repos/XTLS/Xray-core/releases/latest' | grep -o '"browser_download_url": "[^"]*linux-64[^"]*"' | head -3