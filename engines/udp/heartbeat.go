package main

// Heartbeat périodique vers le serveur central de licences (voir dépôt
// LABOSURF_LICENSE_MAKER, server/main.go). Purement informatif :
//   - permet à LICENSE_MAKER d'afficher ACTIVE/ONLINE ou ACTIVE/OFFLINE
//     (option 2 du menu, voir licenseserver_client.go côté Maker) ;
//   - si le serveur répond "REVOKED", ce service journalise un
//     avertissement et pose un drapeau local qui bloquera le PROCHAIN
//     démarrage du service (voir licenseRevokedFlagPath / startup
//     check dans runServerContext) — décision explicite : ne JAMAIS
//     couper le service en cours d'exécution sur simple détection de
//     révocation, pour ne pas interrompre brutalement les clients
//     finaux de l'opérateur.
//
// Une coupure réseau (heartbeat qui échoue) n'a AUCUN effet négatif :
// ni log d'erreur alarmant, ni drapeau, ni interruption. Seule une
// réponse EXPLICITE "REVOKED" du serveur déclenche le drapeau.
//
// Désactivé par défaut : ne s'active que si LABOSURF_LICENSE_SERVER_URL
// est renseignée (voir labosurf-pro.sh, install_service_unit()).

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	heartbeatInterval    = 5 * time.Minute
	heartbeatFirstDelay  = 30 * time.Second
	heartbeatHTTPTimeout = 10 * time.Second
)

// licenseRevokedFlagPath, isLicenseRevoked, markLicenseRevoked et
// licenseKeysWithReceipts prennent un dir explicite (vide = défaut),
// comme receiptPathFor/ListReceipts plus haut dans ce fichier — permet
// de les tester sans écrire dans le vrai /etc/labosurf (root requis).
func licenseRevokedFlagPath(dir string) string {
	if dir == "" {
		dir = defaultReceiptDir
	}
	return filepath.Join(dir, ".license_revoked")
}

// isLicenseRevoked indique si une révocation a été détectée par un
// heartbeat précédent — vérifié une seule fois, au démarrage du service.
func isLicenseRevoked(dir string) bool {
	_, err := os.Stat(licenseRevokedFlagPath(dir))
	return err == nil
}

// markLicenseRevoked pose le drapeau de révocation, de façon idempotente.
func markLicenseRevoked(dir, licenseID string) {
	msg := "Licence " + licenseID + " révoquée (détecté le " + time.Now().UTC().Format(time.RFC3339) + ").\n" +
		"Le service en cours n'a PAS été interrompu. Il ne pourra pas redémarrer\n" +
		"tant que ce fichier existe. Contactez l'administrateur LABOSURF pour une\n" +
		"nouvelle licence, puis supprimez ce fichier après régularisation.\n"
	_ = os.WriteFile(licenseRevokedFlagPath(dir), []byte(msg), 0o644)
}

// licenseKeysWithReceipts retourne les clés de 40 caractères des reçus
// d'installation locaux (vide si aucun reçu ne porte de Key — reçu
// écrit avant l'introduction du serveur central, ou serveur non utilisé
// au moment de l'activation : le heartbeat n'a alors simplement rien à
// envoyer, sans erreur).
func licenseKeysWithReceipts(dir string) []string {
	recs, err := ListReceipts(dir)
	if err != nil {
		return nil
	}
	var keys []string
	for _, r := range recs {
		if strings.TrimSpace(r.Key) != "" {
			keys = append(keys, r.Key)
		}
	}
	return keys
}

func sendHeartbeat(serverURL, receiptDir, key string) {
	body, _ := json.Marshal(map[string]string{"key": key})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(serverURL, "/")+"/v1/heartbeat", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: heartbeatHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// Panne réseau : silencieux, jamais de drapeau. Voir commentaire
		// de package plus haut.
		return
	}
	defer resp.Body.Close()

	var out struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return
	}
	if out.OK && out.Status == "REVOKED" {
		log.Printf("⚠️  ATTENTION : la licence de cette installation a été révoquée par l'administrateur (clé %s...).", safePrefix(key, 12))
		log.Printf("    Le service continue de tourner, mais ne pourra pas redémarrer tant que ce n'est pas régularisé.")
		markLicenseRevoked(receiptDir, key)
	}
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// runLicenseHeartbeat démarre la boucle de heartbeat périodique, si
// LABOSURF_LICENSE_SERVER_URL est configurée. S'arrête proprement sur
// ctx.Done(). N'est jamais appelé si aucune clé n'est trouvée (aucun
// effet, aucun log).
func runLicenseHeartbeat(ctx context.Context) {
	serverURL := strings.TrimSpace(os.Getenv("LABOSURF_LICENSE_SERVER_URL"))
	if serverURL == "" {
		return
	}
	keys := licenseKeysWithReceipts("")
	if len(keys) == 0 {
		return
	}

	tick := func() {
		for _, k := range keys {
			sendHeartbeat(serverURL, "", k)
		}
	}

	timer := time.NewTimer(heartbeatFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick()
			timer.Reset(heartbeatInterval)
		}
	}
}
