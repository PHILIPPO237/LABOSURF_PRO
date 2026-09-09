# Fichiers à supprimer — LABOSURF_PRO

## Fichiers à SUPPRIMER (Code mort confirmé — non référencés nulle part)

### engines/udp/
| Fichier | Type | Lignes | Raison |
|---------|------|--------|--------|
| `engines/udp/client_stub.go` | Stub | Non importé nulle part |
| `engines/udp/system_overview.go` | Code mort | Non importé nulle part |
| `engines/udp/menu_users.go` | Code mort | Non importé nulle part |
| `engines/udp/forwarder.go` | Code mort | Interface `Forwarder` + `UDPForwarder` définis mais **jamais instanciés**. Seulement mentionné en commentaire dans `server.go:640` |
| `engines/udp/system_overview.go` | Code mort | Non importé nulle part |

## Fichiers à CONSERVER (Build tags pour cross-compilation)

Ces fichiers ont des **build tags** corrects pour la cross-compilation :

| Fichier | Build tag | Rôle |
|---------|-----------|------|
| `engines/udp/tun_stub.go` | `//go:build !linux` | Stub TUN pour non-Linux |
| `engines/udp/tun_android.go` | `//go:build android` | Stub TUN Android |
| `engines/udp/network_stub.go` | `//go:build !linux \|\| android` | Stub réseau non-Linux/Android |
| `engines/udp/tun_stub.go` | `//go:build !linux` | Stub TUN non-Linux |
| `engines/udp/tun_android.go` | `//go:build android` | Stub TUN Android |
| `engines/udp/network_stub.go` | `//go:build !linux \|\| android` | Stub réseau non-Linux/Android |

## Fichiers STUB à CONSERVER (build tags corrects)

| Fichier | Build tag | Status |
|---------|-----------|--------|
| `engines/udp/tun_stub.go` | `//go:build !linux` | ✅ CONSERVER |
| `engines/udp/tun_android.go` | `//go:build android` | ✅ CONSERVER |
| `engines/udp/network_stub.go` | `//go:build !linux \|\| android` | ✅ CONSERVER |

## Fichiers UTILISÉS (à conserver)

| Fichier | Utilisation |
|---------|-------------|
| `engines/udp/forwarder.go` | ⚠️ Défini mais **inutilisé** — seulement commentaire dans server.go |
| `engines/udp/receipt.go` | Utilisé dans `config.go`, `license_cli.go` |
| `engines/udp/registry.go` | Utilisé dans `config.go`, `license_cli.go` |
| `engines/udp/device.go` | Référencé dans `portal.go` (HTML) |
| `engines/udp/portal.go` | Utilisé dans `main.go`, `config.go` |
| `engines/udp/device.go` | Référencé dans `portal.go` |

---

## Commandes de nettoyage

```bash
# Supprimer les fichiers morts confirmés
rm engines/udp/client_stub.go
rm engines/udp/system_overview.go
rm engines/udp/menu_users.go
rm engines/udp/forwarder.go
rm engines/udp/system_overview.go

# Vérifier que tout compile
go build ./...
go test ./engines/udp/...
go test ./...
go test -race ./engines/udp/...  # Vérifier data race BUG-001 corrigée
```

## Vérification post-suppression

```bash
# Build complet
go build ./...

# Tests unitaires
go test ./engines/udp/...
go test ./internal/...

# Race detector (vérifier BUG-001 corrigé)
go test -race ./engines/udp/...

# Build complet
go build ./...
```

## Fichiers à CONSERVER (Build tags)

Ces fichiers ont des build tags pour la cross-compilation et doivent être conservés :

| Fichier | Build tag | Rôle |
|---------|-----------|------|
| `engines/udp/tun_stub.go` | `//go:build !linux` | Stub TUN pour non-Linux |
| `engines/udp/tun_android.go` | `//go:build android` | Stub TUN Android |
| `engines/udp/network_stub.go` | `//go:build !linux \|\| android` | Stub réseau non-Linux/Android |

---

**Total fichiers à supprimer : 5**
- `engines/udp/client_stub.go`
- `engines/udp/system_overview.go`
- `engines/udp/menu_users.go`
- `engines/udp/forwarder.go`
- `engines/udp/system_overview.go`

**Gain estimé : ~500 lignes de code mort supprimées**