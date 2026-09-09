import os
import subprocess

files_to_check = [
    'engines/udp/client_stub.go',
    'engines/udp/network_stub.go',
    'engines/udp/system_overview_stub.go',
    'engines/udp/tun_stub.go',
    'engines/udp/tun_android.go',
    'engines/udp/device_stub.go',
    'engines/udp/forwarder.go',
    'engines/udp/receipt.go',
    'engines/udp/registry.go',
    'engines/udp/device.go',
    'engines/udp/system_overview.go',
    'engines/udp/menu_users.go',
    'engines/udp/portal.go',
    'engines/udp/receipt.go',
    'engines/udp/registry.go',
]

base = '/mnt/c/Users/atsan/OneDrive/Bureau/LABOSURF_PRO'

for f in files_to_check:
    name = os.path.basename(f).replace('.go', '')
    result = subprocess.run(['grep', '-r', name, '--include=*.go', base], 
                          capture_output=True, text=True)
    lines = [l for l in result.stdout.split('\n') if f not in l and '_test.go' not in l]
    print(f'=== {f} ===')
    for l in lines[:5]:
        print(l)
    if not lines:
        print('  -> NOT USED anywhere!')
    print()
