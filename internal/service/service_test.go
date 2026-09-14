package service_test

import (
	"testing"
	"time"

	"labosurf/internal/service"
)

// setDataDir redirige le stockage vers un répertoire temporaire isolé.
func setDataDir(t *testing.T) {
	t.Helper()
	t.Setenv("LABOSURF_DATA_DIR", t.TempDir())
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func newSimpleService(name, engine string, port int) service.Service {
	svc := service.NewService(name, engine, "1.2.3.4", map[string]int{"listen": port})
	svc.TLSMode = "reality"
	return svc
}

func newHybridService(name string, components []string, port int) service.Service {
	svc := service.NewService(name, "dnstt-xray", "1.2.3.4", map[string]int{"listen": port})
	svc.Components = append([]string(nil), components...)
	return svc
}

func newAccess(accountID, serviceID, engine string) service.Access {
	return service.NewAccess(accountID, serviceID, engine)
}

// ── Service — persistance ─────────────────────────────────────────────────────

func TestSaveLoadService(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	if err := service.SaveService(&svc); err != nil {
		t.Fatalf("SaveService : %v", err)
	}

	got, err := service.GetService(svc.ID)
	if err != nil {
		t.Fatalf("GetService : %v", err)
	}
	if got.Name != svc.Name {
		t.Errorf("Name = %q, want %q", got.Name, svc.Name)
	}
	if got.Engine != "xray" {
		t.Errorf("Engine = %q, want xray", got.Engine)
	}
	if got.ListenPort() != 443 {
		t.Errorf("ListenPort = %d, want 443", got.ListenPort())
	}
}

func TestGetServiceByName(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("SSH-Backup", "ssh", 22)
	if err := service.SaveService(&svc); err != nil {
		t.Fatalf("SaveService : %v", err)
	}

	got, found, err := service.GetServiceByName("ssh-backup") // insensible à la casse
	if err != nil {
		t.Fatalf("GetServiceByName : %v", err)
	}
	if !found {
		t.Fatal("GetServiceByName devrait trouver le service")
	}
	if got.ID != svc.ID {
		t.Errorf("ID = %q, want %q", got.ID, svc.ID)
	}

	_, found2, _ := service.GetServiceByName("Absent")
	if found2 {
		t.Fatal("GetServiceByName ne devrait rien trouver pour un nom absent")
	}
}

func TestListServices(t *testing.T) {
	setDataDir(t)

	s1 := newSimpleService("Xray-A", "xray", 443)
	s2 := newSimpleService("Xray-B", "xray", 8443)
	s3 := newSimpleService("SSH-Prod", "ssh", 22)

	for i, s := range []*service.Service{&s1, &s2, &s3} {
		if err := service.SaveService(s); err != nil {
			t.Fatalf("[%d] SaveService : %v", i, err)
		}
	}

	all, err := service.ListServices()
	if err != nil {
		t.Fatalf("ListServices : %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListServices len = %d, want 3", len(all))
	}
}

func TestListServicesEmptyDir(t *testing.T) {
	setDataDir(t)

	all, err := service.ListServices()
	if err != nil {
		t.Fatalf("ListServices (répertoire absent) : %v", err)
	}
	if len(all) != 0 {
		t.Errorf("ListServices (répertoire absent) = %d, want 0", len(all))
	}
}

func TestListServicesByEngine(t *testing.T) {
	setDataDir(t)

	s1 := newSimpleService("Xray-Prod", "xray", 443)
	s2 := newSimpleService("Xray-Test", "xray", 8443)
	s3 := newSimpleService("SSH-Prod", "ssh", 22)

	for _, s := range []*service.Service{&s1, &s2, &s3} {
		_ = service.SaveService(s)
	}

	xrayServices, err := service.ListServicesByEngine("xray")
	if err != nil {
		t.Fatalf("ListServicesByEngine : %v", err)
	}
	if len(xrayServices) != 2 {
		t.Errorf("ListServicesByEngine(xray) = %d, want 2", len(xrayServices))
	}

	sshServices, _ := service.ListServicesByEngine("ssh")
	if len(sshServices) != 1 {
		t.Errorf("ListServicesByEngine(ssh) = %d, want 1", len(sshServices))
	}

	wgServices, _ := service.ListServicesByEngine("wireguard")
	if len(wgServices) != 0 {
		t.Errorf("ListServicesByEngine(wireguard) = %d, want 0", len(wgServices))
	}
}

func TestDeleteServiceBlockedByAccess(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	acc := newAccess("alice", svc.ID, "xray")
	_ = service.SaveAccess(&acc)

	err := service.DeleteService(svc.ID)
	if err == nil {
		t.Fatal("DeleteService devrait refuser si des Access existent")
	}
}

func TestDeleteServiceOK(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("SSH-Test", "ssh", 22)
	_ = service.SaveService(&svc)

	if err := service.DeleteService(svc.ID); err != nil {
		t.Fatalf("DeleteService (sans Access) : %v", err)
	}

	_, err := service.GetService(svc.ID)
	if err == nil {
		t.Fatal("GetService devrait retourner ErrServiceNotFound après suppression")
	}
}

func TestDeleteServiceNotFound(t *testing.T) {
	setDataDir(t)

	err := service.DeleteService("inexistant")
	if err == nil {
		t.Fatal("DeleteService d'un ID absent devrait retourner une erreur")
	}
}

// ── Access — persistance ──────────────────────────────────────────────────────

func TestSaveLoadAccess(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	acc := newAccess("alice", svc.ID, "xray")
	acc.QuotaUnlimited = false
	acc.QuotaLimitBytes = 10 * 1024 * 1024 * 1024 // 10 Go
	acc.MaxDevices = 2
	acc.MaxConnections = 4

	if err := service.SaveAccess(&acc); err != nil {
		t.Fatalf("SaveAccess : %v", err)
	}

	got, err := service.GetAccess(acc.ID)
	if err != nil {
		t.Fatalf("GetAccess : %v", err)
	}
	if got.AccountID != "alice" {
		t.Errorf("AccountID = %q, want alice", got.AccountID)
	}
	if got.QuotaLimitBytes != 10*1024*1024*1024 {
		t.Errorf("QuotaLimitBytes = %d", got.QuotaLimitBytes)
	}
	if got.MaxDevices != 2 {
		t.Errorf("MaxDevices = %d, want 2", got.MaxDevices)
	}
}

func TestListAccessByAccount(t *testing.T) {
	setDataDir(t)

	s1 := newSimpleService("Xray", "xray", 443)
	s2 := newSimpleService("SSH", "ssh", 22)
	_ = service.SaveService(&s1)
	_ = service.SaveService(&s2)

	// alice a deux accès
	a1 := newAccess("alice", s1.ID, "xray")
	a2 := newAccess("alice", s2.ID, "ssh")
	// bob en a un
	a3 := newAccess("bob", s1.ID, "xray")

	for _, a := range []*service.Access{&a1, &a2, &a3} {
		_ = service.SaveAccess(a)
	}

	aliceAccess, err := service.ListAccessByAccount("alice")
	if err != nil {
		t.Fatalf("ListAccessByAccount : %v", err)
	}
	if len(aliceAccess) != 2 {
		t.Errorf("alice devrait avoir 2 accès, obtenu %d", len(aliceAccess))
	}

	bobAccess, _ := service.ListAccessByAccount("bob")
	if len(bobAccess) != 1 {
		t.Errorf("bob devrait avoir 1 accès, obtenu %d", len(bobAccess))
	}

	noAccess, _ := service.ListAccessByAccount("charlie")
	if len(noAccess) != 0 {
		t.Errorf("charlie devrait avoir 0 accès, obtenu %d", len(noAccess))
	}
}

func TestListAccessByService(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	a1 := newAccess("alice", svc.ID, "xray")
	a2 := newAccess("bob", svc.ID, "xray")
	_ = service.SaveAccess(&a1)
	_ = service.SaveAccess(&a2)

	accesses, err := service.ListAccessByService(svc.ID)
	if err != nil {
		t.Fatalf("ListAccessByService : %v", err)
	}
	if len(accesses) != 2 {
		t.Errorf("ListAccessByService = %d, want 2", len(accesses))
	}
}

func TestAccessExists(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	acc := newAccess("alice", svc.ID, "xray")
	_ = service.SaveAccess(&acc)

	exists, id, err := service.AccessExists("alice", svc.ID)
	if err != nil {
		t.Fatalf("AccessExists : %v", err)
	}
	if !exists {
		t.Fatal("AccessExists devrait retourner true")
	}
	if id != acc.ID {
		t.Errorf("id = %q, want %q", id, acc.ID)
	}

	// Bob n'a pas d'accès
	exists2, _, _ := service.AccessExists("bob", svc.ID)
	if exists2 {
		t.Fatal("AccessExists devrait retourner false pour bob")
	}
}

func TestDuplicateAccess(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	a1 := newAccess("alice", svc.ID, "xray")
	_ = service.SaveAccess(&a1)

	// Détection du doublon avant insertion
	exists, _, err := service.AccessExists("alice", svc.ID)
	if err != nil {
		t.Fatalf("AccessExists : %v", err)
	}
	if !exists {
		t.Fatal("AccessExists devrait détecter le doublon")
	}
}

func TestDeleteAccess(t *testing.T) {
	setDataDir(t)

	svc := newSimpleService("Xray-Prod", "xray", 443)
	_ = service.SaveService(&svc)

	acc := newAccess("alice", svc.ID, "xray")
	_ = service.SaveAccess(&acc)

	if err := service.DeleteAccess(acc.ID); err != nil {
		t.Fatalf("DeleteAccess : %v", err)
	}

	_, err := service.GetAccess(acc.ID)
	if err == nil {
		t.Fatal("GetAccess devrait retourner ErrAccessNotFound après suppression")
	}
}

func TestDeleteAccessNotFound(t *testing.T) {
	setDataDir(t)

	err := service.DeleteAccess("inexistant")
	if err == nil {
		t.Fatal("DeleteAccess d'un ID absent devrait retourner une erreur")
	}
}

// ── Validations Service ───────────────────────────────────────────────────────

func TestServiceValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*service.Service)
		wantErr bool
	}{
		{
			name:    "valide simple",
			mutate:  func(s *service.Service) {},
			wantErr: false,
		},
		{
			name:    "ID vide",
			mutate:  func(s *service.Service) { s.ID = "" },
			wantErr: true,
		},
		{
			name:    "Name vide",
			mutate:  func(s *service.Service) { s.Name = "" },
			wantErr: true,
		},
		{
			name:    "Engine vide",
			mutate:  func(s *service.Service) { s.Engine = "" },
			wantErr: true,
		},
		{
			name:    "port négatif",
			mutate:  func(s *service.Service) { s.Ports["listen"] = -1 },
			wantErr: true,
		},
		{
			name:    "port > 65535",
			mutate:  func(s *service.Service) { s.Ports["listen"] = 70000 },
			wantErr: true,
		},
		{
			name: "hybride 1 composant seulement",
			mutate: func(s *service.Service) {
				s.Engine = "dnstt-xray"
				s.Components = []string{"xray"}
			},
			wantErr: true,
		},
		{
			name: "hybride 2 composants valide",
			mutate: func(s *service.Service) {
				s.Engine = "dnstt-xray"
				s.Components = []string{"dnstt", "xray"}
			},
			wantErr: false,
		},
		{
			name: "hybride avec composant vide",
			mutate: func(s *service.Service) {
				s.Engine = "dnstt-xray"
				s.Components = []string{"dnstt", ""}
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.NewService("Test", "xray", "1.2.3.4", map[string]int{"listen": 443})
			tc.mutate(&svc)
			err := service.ValidateService(&svc)
			if tc.wantErr && err == nil {
				t.Error("ValidateService devrait retourner une erreur")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateService erreur inattendue : %v", err)
			}
		})
	}
}

// ── Validations Access ────────────────────────────────────────────────────────

func TestAccessValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*service.Access)
		wantErr bool
	}{
		{
			name:    "valide illimité",
			mutate:  func(a *service.Access) {},
			wantErr: false,
		},
		{
			name:    "ID vide",
			mutate:  func(a *service.Access) { a.ID = "" },
			wantErr: true,
		},
		{
			name:    "AccountID vide",
			mutate:  func(a *service.Access) { a.AccountID = "" },
			wantErr: true,
		},
		{
			name:    "ServiceID vide",
			mutate:  func(a *service.Access) { a.ServiceID = "" },
			wantErr: true,
		},
		{
			name:    "Engine vide",
			mutate:  func(a *service.Access) { a.Engine = "" },
			wantErr: true,
		},
		{
			name:    "MaxDevices négatif",
			mutate:  func(a *service.Access) { a.MaxDevices = -1 },
			wantErr: true,
		},
		{
			name:    "MaxConnections négatif",
			mutate:  func(a *service.Access) { a.MaxConnections = -1 },
			wantErr: true,
		},
		{
			name:    "ExpiresAt invalide",
			mutate:  func(a *service.Access) { a.ExpiresAt = "pas-une-date" },
			wantErr: true,
		},
		{
			name: "ExpiresAt valide",
			mutate: func(a *service.Access) {
				a.ExpiresAt = time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc := service.NewAccess("alice", "svc-1", "xray")
			tc.mutate(&acc)
			err := service.ValidateAccess(&acc)
			if tc.wantErr && err == nil {
				t.Error("ValidateAccess devrait retourner une erreur")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateAccess erreur inattendue : %v", err)
			}
		})
	}
}

// ── Quota ─────────────────────────────────────────────────────────────────────

func TestQuotaUnlimited(t *testing.T) {
	acc := service.NewAccess("alice", "svc", "xray")
	// QuotaUnlimited=true par défaut dans NewAccess

	if !acc.QuotaUnlimited {
		t.Fatal("NewAccess devrait créer un accès illimité par défaut")
	}
	if acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait retourner false quand QuotaUnlimited=true")
	}
	// Même si UsedBytes est très grand, illimité reste illimité
	acc.UsedBytes = 1 << 60
	if acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait rester false avec QuotaUnlimited=true")
	}

	// Validation : illimité est toujours valide
	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (illimité) : %v", err)
	}
}

func TestQuotaLimited(t *testing.T) {
	acc := service.NewAccess("alice", "svc", "xray")
	acc.QuotaUnlimited = false
	acc.QuotaLimitBytes = 5 * 1024 * 1024 * 1024 // 5 Go

	if acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait être false avec 0 octets utilisés")
	}

	acc.UsedBytes = int64(acc.QuotaLimitBytes) - 1
	if acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait être false sous la limite")
	}

	acc.UsedBytes = int64(acc.QuotaLimitBytes)
	if !acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait être true à la limite exacte")
	}

	acc.UsedBytes = int64(acc.QuotaLimitBytes) + 1024
	if !acc.QuotaExceeded() {
		t.Fatal("QuotaExceeded devrait être true au-delà de la limite")
	}

	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (limité, QuotaLimitBytes > 0) : %v", err)
	}
}

func TestQuotaLimitedZeroRejected(t *testing.T) {
	// QuotaUnlimited=false avec QuotaLimitBytes=0 doit être refusé à la validation.
	acc := service.NewAccess("alice", "svc", "xray")
	acc.QuotaUnlimited = false
	acc.QuotaLimitBytes = 0

	if err := service.ValidateAccess(&acc); err == nil {
		t.Fatal("ValidateAccess devrait refuser QuotaUnlimited=false + QuotaLimitBytes=0")
	}
}

// ── MaxDevices / MaxConnections ───────────────────────────────────────────────

func TestMaxDevices(t *testing.T) {
	acc := service.NewAccess("alice", "svc", "xray")
	acc.MaxDevices = 2
	acc.MaxConnections = 4

	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (MaxDevices=2) : %v", err)
	}

	acc.MaxDevices = 0 // illimité
	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (MaxDevices=0, illimité) : %v", err)
	}
}

func TestMaxConnections(t *testing.T) {
	acc := service.NewAccess("alice", "svc", "xray")
	acc.MaxConnections = 5
	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (MaxConnections=5) : %v", err)
	}

	acc.MaxConnections = 0 // illimité
	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (MaxConnections=0, illimité) : %v", err)
	}
}

// ── Expiration ────────────────────────────────────────────────────────────────

func TestExpirationValidation(t *testing.T) {
	acc := service.NewAccess("alice", "svc", "xray")

	// Vide = pas d'expiration
	acc.ExpiresAt = ""
	if err := service.ValidateAccess(&acc); err != nil {
		t.Errorf("ValidateAccess (ExpiresAt vide) : %v", err)
	}
	if acc.IsExpired() {
		t.Fatal("IsExpired devrait être false avec ExpiresAt vide")
	}

	// Date dans le passé
	acc.ExpiresAt = time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	if !acc.IsExpired() {
		t.Fatal("IsExpired devrait être true pour une date passée")
	}

	// Date dans le futur
	acc.ExpiresAt = time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	if acc.IsExpired() {
		t.Fatal("IsExpired devrait être false pour une date future")
	}
}

// ── Service hybride ───────────────────────────────────────────────────────────

func TestHybridService(t *testing.T) {
	setDataDir(t)

	svc := newHybridService("DNSTT-Xray-Prod", []string{"dnstt", "xray"}, 443)
	svc.Domains = []string{"tunnel.exemple.tld"}

	if !svc.IsHybrid() {
		t.Fatal("IsHybrid devrait être true")
	}
	if err := service.ValidateService(&svc); err != nil {
		t.Fatalf("ValidateService (hybride) : %v", err)
	}
	if err := service.SaveService(&svc); err != nil {
		t.Fatalf("SaveService (hybride) : %v", err)
	}

	// Un seul Access pour ce service hybride
	acc := newAccess("alice", svc.ID, "dnstt-xray")
	// L'Access contient les secrets des deux composants dans le même map
	acc.Secrets["uuid"] = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	acc.Secrets["public_key"] = "aabbccddeeff"
	acc.Secrets["private_key"] = "112233445566"

	if err := service.SaveAccess(&acc); err != nil {
		t.Fatalf("SaveAccess (hybride) : %v", err)
	}

	// Vérification : 1 seul Access pour le service hybride
	accesses, err := service.ListAccessByService(svc.ID)
	if err != nil {
		t.Fatalf("ListAccessByService : %v", err)
	}
	if len(accesses) != 1 {
		t.Errorf("len(accesses) = %d, want 1 (un seul Access pour un hybride)", len(accesses))
	}

	// Les secrets des deux composants sont bien dans le même Access
	got, _ := service.GetAccess(acc.ID)
	if got.Secrets["uuid"] != "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" {
		t.Error("secret uuid non persisté dans l'Access hybride")
	}
	if got.Secrets["public_key"] != "aabbccddeeff" {
		t.Error("secret public_key non persisté dans l'Access hybride")
	}
}

// ── Écriture atomique ─────────────────────────────────────────────────────────

func TestSaveServiceUpdateTime(t *testing.T) {
	setDataDir(t)

	svc := service.NewService("Xray-Prod", "xray", "1.2.3.4", map[string]int{"listen": 443})
	_ = service.SaveService(&svc)

	t1 := svc.UpdatedAt

	// Modification et re-sauvegarde
	svc.Name = "Xray-Production"
	_ = service.SaveService(&svc)

	got, _ := service.GetService(svc.ID)
	if !got.UpdatedAt.After(t1) {
		t.Error("UpdatedAt devrait être mis à jour après SaveService")
	}
}
