package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"
)

type portalServer struct {
	store    *Store
	sessions *SessionManager // état RÉEL du moteur UDP Engine (peut être nil)
	mux      *http.ServeMux
	addr     string
	srv      *http.Server
}

// NewPortalServer construit un portail autonome qui lit le store sur
// disque. Sans SessionManager, les données live (trafic, connexions, IP)
// ne sont pas disponibles : préférer NewPortalServerShared.
func NewPortalServer(addr string, storePath string) (*portalServer, error) {
	s, err := LoadStore(storePath)
	if err != nil {
		return nil, err
	}
	return NewPortalServerShared(addr, s, nil), nil
}

// NewPortalServerShared construit un portail qui PARTAGE le store et le
// SessionManager du moteur UDP Engine. C'est la voie utilisée lorsque le
// portail tourne dans le processus UDP Engine : il n'existe alors qu'un seul
// état de sessions dans tout le système.
func NewPortalServerShared(addr string, store *Store, sessions *SessionManager) *portalServer {
	ps := &portalServer{store: store, sessions: sessions, addr: addr}
	ps.mux = http.NewServeMux()
	ps.mux.HandleFunc("/", ps.handleIndex)
	ps.mux.HandleFunc("/client/", ps.handleClient)
	ps.mux.HandleFunc("/api/usage", ps.handleAPIUsage)
	return ps
}

// liveAccount enrichit un compte avec les données réelles produites par le
// moteur UDP Engine (trafic consommé, connexions actives, IP distinctes).
func (ps *portalServer) liveAccount(acc Account) Account {
	if ps.sessions == nil {
		return acc
	}

	_, _, total := ps.sessions.UsageByUser(acc.Username)
	if total > 0 {
		acc.UsedBytes = clampToInt64(total)
	}

	count, ips := ps.sessions.ActiveIPs(acc.Username)
	acc.CurrentConns = count
	acc.CurrentIPs = ips

	return acc
}

// clampToInt64 convertit un uint64 en int64 sans débordement.
func clampToInt64(v uint64) int64 {
	const maxInt64 = uint64(1)<<63 - 1
	if v > maxInt64 {
		return int64(maxInt64)
	}
	return int64(v)
}

// ListenAndServe démarre le portail et s'arrête proprement à l'annulation
// du context (fermeture HTTP gracieuse, aucune goroutine bloquée).
func (ps *portalServer) ListenAndServe(ctx context.Context) error {
	ps.srv = &http.Server{
		Addr:              ps.addr,
		Handler:           ps.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)

	go func() {
		log.Printf("Portail HTTP démarré sur %s", ps.addr)
		err := ps.srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := ps.srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Arrêt du portail : %v", err)
		}

		<-errCh // la goroutine se termine toujours après Shutdown
		log.Println("Portail HTTP arrêté.")
		return nil

	case err := <-errCh:
		return err
	}
}

func (ps *portalServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

func (ps *portalServer) handleClient(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/client/")
	if token == "" {
		http.NotFound(w, r)
		return
	}

	acc, ok := ps.store.GetByToken(token)
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(notFoundHTML))
		return
	}

	// Enrichissement avec les données réelles du moteur UDP Engine.
	acc = ps.liveAccount(acc)

	data := buildPortalData(acc)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := clientTmpl.Execute(w, data); err != nil {
		log.Printf("erreur rendu portail : %v", err)
	}
}

// handleAPIUsage retourne les données de consommation en temps réel pour
// un token donné. Format JSON. Usage : /api/usage?token=<token>
func (ps *portalServer) handleAPIUsage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, `{"error":"token requis"}`, http.StatusBadRequest)
		return
	}

	acc, ok := ps.store.GetByToken(token)
	if !ok {
		http.Error(w, `{"error":"token invalide"}`, http.StatusNotFound)
		return
	}

	acc = ps.liveAccount(acc)

	type usageResponse struct {
		ID             string   `json:"id"`
		Enabled        bool     `json:"enabled"`
		ExpiresAt      string   `json:"expires_at,omitempty"`
		UsedBytes      int64    `json:"used_bytes"`
		QuotaBytes     uint64   `json:"quota_bytes"`
		MaxConnections int      `json:"max_connections"`
		CurrentConns   int      `json:"current_connections"`
		MaxIPs         int      `json:"max_ips"`
		CurrentIPs     []string `json:"current_ips"`
	}

	// SÉCURITÉ : ne jamais exposer le mot de passe ni le jeton de licence.
	resp := usageResponse{
		ID:             acc.ID,
		Enabled:        acc.Enabled,
		ExpiresAt:      acc.ExpiresAt,
		UsedBytes:      acc.UsedBytes,
		QuotaBytes:     acc.QuotaBytes,
		MaxConnections: acc.MaxConnections,
		CurrentConns:   acc.CurrentConns,
		MaxIPs:         acc.MaxIPs,
		CurrentIPs:     acc.CurrentIPs,
	}

	if resp.CurrentIPs == nil {
		resp.CurrentIPs = []string{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func buildPortalData(acc Account) portalData {
	pd := portalData{
		ID:             acc.ID,
		Username:       acc.Username,
		Token:          acc.Token,
		Enabled:        acc.Enabled,
		OfferID:        acc.OfferID,
		QuotaBytes:     int64(acc.QuotaBytes),
		UsedBytes:      acc.UsedBytes,
		MaxConnections: acc.MaxConnections,
		CurrentIPs:     len(acc.CurrentIPs),
		MaxIPs:         acc.MaxIPs,
	}

	// CurrentConns est passé directement depuis le champ Count si disponible.
	pd.CurrentConns = acc.CurrentConns
	if pd.CurrentConns == 0 {
		pd.CurrentConns = len(acc.CurrentIPs)
	}

	if acc.ExpiresAt == "" {
		pd.ExpiryLabel = "Illimité"
		pd.ExpiryWarn = false
	} else {
		exp, err := time.Parse(time.RFC3339, acc.ExpiresAt)
		if err == nil {
			dur := time.Until(exp)
			if dur <= 0 {
				pd.ExpiryLabel = "Expiré"
				pd.ExpiryWarn = true
				pd.Expired = true
			} else {
				days := int(dur.Hours() / 24)
				hours := int(dur.Hours()) % 24
				if days > 0 {
					pd.ExpiryLabel = exp.Format("2006-01-02") + " (" + fmt.Sprintf("%dj %dh", days, hours) + ")"
				} else {
					pd.ExpiryLabel = exp.Format("2006-01-02 15:04") + " (" + fmt.Sprintf("%dh", hours) + ")"
				}
				pd.ExpiryWarn = days < 7
			}
		} else {
			pd.ExpiryLabel = acc.ExpiresAt
		}
	}

	if acc.QuotaBytes > 0 {
		pd.QuotaUsedPercent = int(float64(acc.UsedBytes) / float64(acc.QuotaBytes) * 100)
		if pd.QuotaUsedPercent > 100 {
			pd.QuotaUsedPercent = 100
		}
		pd.QuotaWarn = pd.QuotaUsedPercent > 80
		pd.QuotaLabel = fmt.Sprintf("%s / %s", formatBytesPortal(acc.UsedBytes), formatBytesPortal(int64(acc.QuotaBytes)))
	} else {
		pd.QuotaLabel = fmt.Sprintf("%s (illimité)", formatBytesPortal(acc.UsedBytes))
		pd.QuotaUsedPercent = 0
	}

	if acc.Enabled {
		pd.StateLabel = "Actif"
		pd.StateClass = "ok"
	} else {
		pd.StateLabel = "Inactif"
		pd.StateClass = "warn"
		pd.StateWarn = true
	}

	return pd
}

type portalData struct {
	ID, Username, Token string
	Enabled             bool
	OfferID             string

	ExpiryLabel string
	ExpiryWarn  bool
	Expired     bool

	QuotaBytes       int64
	UsedBytes        int64
	QuotaLabel       string
	QuotaUsedPercent int
	QuotaWarn        bool

	MaxConnections int
	CurrentConns   int
	CurrentIPs     int
	MaxIPs         int

	StateLabel string
	StateClass string
	StateWarn  bool
}

func formatBytesPortal(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="fr">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>LABOSURF PRO — Portail</title>
  <style>
    *{box-sizing:border-box;margin:0;padding:0}
    body{font-family:system-ui,-apple-system,sans-serif;background:#0a0e17;color:#e2e8f0;min-height:100vh;display:flex;align-items:center;justify-content:center}
    .wrap{max-width:560px;width:100%;padding:2rem}
    h1{font-size:1.75rem;text-align:center;margin-bottom:.5rem;background:linear-gradient(135deg,#00d4ff,#00ff88);-webkit-background-clip:text;-webkit-text-fill-color:transparent}
    .sub{text-align:center;color:#64748b;margin-bottom:2rem}
  </style>
</head>
<body>
  <div class="wrap">
    <h1>LABOSURF PRO</h1>
    <p class="sub">Accédez à votre portail via le lien unique fourni par l'administrateur.</p>
  </div>
</body>
</html>`

const notFoundHTML = `<!DOCTYPE html>
<html lang="fr">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Lien invalide — LABOSURF PRO</title>
  <style>
    *{box-sizing:border-box;margin:0;padding:0}
    body{font-family:system-ui,-apple-system,sans-serif;background:#0a0e17;color:#e2e8f0;min-height:100vh;display:flex;align-items:center;justify-content:center}
    .wrap{max-width:560px;width:100%;padding:2rem;text-align:center}
    h1{font-size:1.75rem;color:#ef4444;margin-bottom:.5rem}
    .sub{color:#64748b;margin-bottom:1rem}
  </style>
</head>
<body>
  <div class="wrap">
    <h1>Lien invalide</h1>
    <p class="sub">Ce lien client n'existe pas ou a été révoqué.</p>
  </div>
</body>
</html>`

var clientTmpl = template.Must(template.New("client").Parse(`<!DOCTYPE html>
<html lang="fr">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.ID}} — LABOSURF PRO</title>
  <style>
    *{box-sizing:border-box;margin:0;padding:0}
    body{font-family:system-ui,-apple-system,sans-serif;background:#0a0e17;color:#e2e8f0;min-height:100vh}
    .wrap{max-width:640px;width:100%;margin:0 auto;padding:2rem 1.5rem}
    h1{font-size:1.6rem;background:linear-gradient(135deg,#00d4ff,#00ff88);-webkit-background-clip:text;-webkit-text-fill-color:transparent;margin-bottom:1.5rem}
    .card{background:#111827;border:1px solid #1e293b;border-radius:12px;padding:1.25rem;margin-bottom:1rem}
    .label{font-size:.75rem;color:#64748b;text-transform:uppercase;letter-spacing:.05em;margin-bottom:.25rem}
    .value{font-size:1.1rem}
    .ok{color:#00ff88}.warn{color:#f59e0b}.err{color:#ef4444}
    .bar-bg{background:#1e293b;border-radius:6px;height:8px;margin-top:.5rem}
    .bar-fill{height:100%;border-radius:6px;transition:width .3s}
    .bar-ok{background:linear-gradient(90deg,#00d4ff,#00ff88)}
    .bar-warn{background:linear-gradient(90deg,#f59e0b,#ef4444)}
    .row{display:flex;gap:1rem;flex-wrap:wrap}
    .col{flex:1;min-width:120px}
    .footer{text-align:center;color:#475569;font-size:.75rem;margin-top:2rem}
  </style>
</head>
<body>
  <div class="wrap">
    <h1>{{.ID}}</h1>
    <div class="card">
      <div class="label">État</div>
      <div class="value {{.StateClass}}">{{.StateLabel}}</div>
    </div>
    <div class="card">
      <div class="label">Expiration</div>
      <div class="value {{if .Expired}}err{{else if .ExpiryWarn}}warn{{else}}ok{{end}}">{{.ExpiryLabel}}</div>
    </div>
    <div class="card">
      <div class="label">Trafic consommé</div>
      <div class="value">{{.QuotaLabel}}</div>
      {{if gt .QuotaBytes 0}}<div class="bar-bg"><div class="bar-fill {{if .QuotaWarn}}bar-warn{{else}}bar-ok{{end}}" style="width:{{.QuotaUsedPercent}}%"></div></div>{{end}}
    </div>
    <div class="row">
      <div class="col card">
        <div class="label">Connexions</div>
        <div class="value">{{.CurrentConns}}/{{.MaxConnections}}</div>
      </div>
      <div class="col card">
        <div class="label">Adresses IP</div>
        <div class="value">{{.CurrentIPs}}/{{.MaxIPs}}</div>
      </div>
    </div>
    <div class="footer">LABOSURF PRO — Portail client</div>
  </div>
</body>
</html>`))
