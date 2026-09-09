# Fichiers inutilisés / Code mort à supprimer — LABOSURF_PRO

## Fichiers COMPLÈTEMENT inutilisés (non référencés nulle part)

### engines/udp/
| Fichier | Type | Raison |
|---------|------|--------|
| `engines/udp/client_stub.go` | Stub | Non importé nulle part |
| `engines/udp/network_stub.go` | Stub | Non importé nulle part |
| `engines/udp/system_overview_stub.go` | Stub | Non utilisé |
| `engines/udp/tun_stub.go` | Stub | Non utilisé (tun_linux.go utilisé) |
| `engines/udp/tun_android.go` | Stub | Non utilisé (Android non supporté) |
| `engines/udp/device_stub.go` | Stub | Non utilisé |
| `engines/udp/system_overview.go` | Code mort | Non importé nulle part |
| `engines/udp/menu_users.go` | Code mort | Non importé nulle part |
| `engines/udp/device_stub.go` | Stub | Non utilisé |
| `engines/udp/forwarder.go` | Code mort | Seulement mentionné en commentaire dans server.go |
| `engines/udp/system_overview_stub.go` | Stub | Non utilisé |

### Fichiers utilisés MAIS potentiellement redondants

| Fichier | Statut | Commentaire |
|---------|--------|-------------|
| `engines/udp/forwarder.go` | Défini mais inutilisé | Interface `Forwarder` + implémentation `UDPForwarder` définies mais jamais instanciées. Seulement mentionné en commentaire dans `server.go:640` |
| `engines/udp/system_overview.go` | Code mort | Non importé nulle part |
| `engines/udp/menu_users.go` | Code mort | Non importé nulle part |

## Fichiers STUB (pour compatibilité build) - À CONSERVER

Ces fichiers existent pour permettre la compilation sur différentes plateformes :

| Fichier | Rôle | Action |
|---------|------|--------|
| `engines/udp/tun_stub.go` | Stub pour builds non-Linux | **CONSERVER** (build tags) |
| `engines/udp/tun_android.go` | Stub Android | **CONSERVER** (build tags `linux && !android` vs `android`) |
| `engines/udp/device_stub.go` | Stub device | **CONSERVER** (build tags) |
| `engines/udp/tun_stub.go` | Stub TUN | **CONSERVER** (build tags) |
| `engines/udp/network_stub.go` | Stub réseau | **CONSERVER** (build tags) |
| `engines/udp/system_overview_stub.go` | Stub système | **CONSERVER** (build tags) |
| `engines/udp/device_stub.go` | Stub device | **CONSERVER** (build tags) |

## Fichiers à SUPPRIMER (Code mort confirmé)

```
engines/udp/client_stub.go
engines/udp/network_stub.go (si pas de build tag)
engines/udp/system_overview_stub.go (si pas de build tag)
engines/udp/tun_stub.go (si pas de build tag)
engines/udp/tun_android.go (si pas de build tag)
engines/udp/device_stub.go (si pas de build tag)
engines/udp/system_overview.go
engines/udp/menu_users.go
engines/udp/forwarder.go
engines/udp/system_overview_stub.go (si pas de build tag)
```

## Fichiers à VÉRIFIER (build tags)

Vérifier si ces fichiers ont des build tags (`//go:build` ou `// +build`) :

```bash
head -5 engines/udp/tun_stub.go
head -5 engines/udp/tun_android.go
head -5 engines/udp/network_stub.go
head -5 engines/udp/device_stub.go
head -5 engines/udp/system_overview_stub.go
```

## Commandes de nettoyage recommandées

```bash
# Fichiers à supprimer (code mort confirmé)
rm engines/udp/client_stub.go
rm engines/udp/system_overview.go
rm engines/udp/menu_users.go
rm engines/udp/forwarder.go
rm engines/udp/system_overview.go

# Après suppression, vérifier que le build passe :
go build ./...
go test ./...
```

## Vérification post-suppression

```bash
# Vérifier que tout compile
go build ./...

# Tests
go test ./engines/udp/...
go test ./internal/...
go test ./...

# Race detector
go test -race ./engines/udp/...
```

## Fichiers dans AUTRES dossiers à vérifier

Vérifier aussi ces dossiers pour du code mort :
- `cmd/labosurf-*/main.go` - tous utilisés ?
- `internal/enginecli/cli.go` - utilisé ?
- `internal/engineutil/` - tous utilisés ?
- `internal/secret/` - utilisé ?