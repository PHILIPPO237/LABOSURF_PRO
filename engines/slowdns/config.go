package slowdns

import (
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	defaultSlowDNSPort = 53
	defaultBackend     = "127.0.0.1:22"
)

type SlowDNSConfig struct {
	Domain  string         `json:"domain"`
	Port    int            `json:"port"`
	Backend string         `json:"backend"`
	Users   []SlowDNSUser  `json:"users"`

	// JitterMs : délai aléatoire maximal (millisecondes) appliqué avant
	// chaque réponse DNS, pour décorréler le rythme des échanges et rester
	// sous les seuils de détection des DPI qui bloquent les tunnels DNS au
	// débit/rythme trop régulier. 0 = pas de jitter (comportement
	// historique). Valeur recommandée : 30-80 ms.
	JitterMs int `json:"jitter_ms,omitempty"`
}

type SlowDNSUser struct {
	User       string `json:"user"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Enabled    bool   `json:"enabled"`
}

func loadSlowDNSConfig(path string) (SlowDNSConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SlowDNSConfig{}, fmt.Errorf("lecture config SlowDNS : %w", err)
	}
	var cfg SlowDNSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return SlowDNSConfig{}, fmt.Errorf("config SlowDNS invalide : %w", err)
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultSlowDNSPort
	}
	if cfg.Backend == "" {
		cfg.Backend = defaultBackend
	}
	return cfg, nil
}

func encodeSubdomain(data []byte) string {
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
	var parts []string
	for i := 0; i < len(encoded); i += 63 {
		end := i + 63
		if end > len(encoded) {
			end = len(encoded)
		}
		parts = append(parts, strings.ToLower(encoded[i:end]))
	}
	return strings.Join(parts, ".")
}

func decodeSubdomain(subdomain string) ([]byte, error) {
	cleaned := strings.ReplaceAll(subdomain, ".", "")
	cleaned = strings.ToUpper(cleaned)
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
}

type DNSPacket struct {
	ID        uint16
	Flags     uint16
	Questions uint16
	Answers   uint16
	Authority uint16
	Additionals uint16
}

func buildDNSResponse(query []byte, answerData []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query)+16+len(answerData))
	copy(resp, query)

	resp[2] = 0x81
	resp[3] = 0x80

	binary.BigEndian.PutUint16(resp[6:], 1)

	nameOffset := 12
	for nameOffset < len(query) && query[nameOffset] != 0 {
		nameOffset += int(query[nameOffset]) + 1
	}
	nameOffset++

	qtype := binary.BigEndian.PutUint16
	_ = qtype

	pos := len(query)
	resp[pos] = 0xC0
	resp[pos+1] = byte(12)
	binary.BigEndian.PutUint16(resp[pos+2:], 16)
	binary.BigEndian.PutUint16(resp[pos+4:], 1)
	binary.BigEndian.PutUint16(resp[pos+6:], 300)
	binary.BigEndian.PutUint16(resp[pos+8:], uint16(len(answerData)))
	copy(resp[pos+10:], answerData)

	return resp[:pos+10+len(answerData)]
}

// buildDNSResponseForPayload construit la réponse DNS pour un paquet de
// données retour (backend -> client). Contrairement à une version antérieure
// de cette fonction, elle exige la VRAIE requête DNS à laquelle elle répond
// (query) : buildDNSResponse en a besoin pour recopier l'en-tête (ID de
// transaction) et le nom de domaine interrogé, sans quoi aucune réponse ne
// peut jamais être construite (buildDNSResponse retourne nil si query fait
// moins de 12 octets, ce qui était systématiquement le cas quand on lui
// passait nil).
//
// sessionID doit être les 16 octets BRUTS de session (pas la représentation
// hexadécimale) : les coller tels quels permet au client de retrouver sa
// session dans la réponse par une simple comparaison d'octets, au lieu
// d'une chaîne hex tronquée à 16 caractères (donc ne représentant que la
// moitié des octets réels de session, dans un mauvais encodage).
func buildDNSResponseForPayload(query []byte, sessionID []byte, payload []byte) []byte {
	responseData := make([]byte, len(sessionID)+len(payload))
	copy(responseData, sessionID)
	copy(responseData[len(sessionID):], payload)

	return buildDNSResponse(query, responseData)
}