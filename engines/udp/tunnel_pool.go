package main

import (
	"errors"
	"net"
	"sync"
)

var (
	ErrTunnelIPPoolExhausted = errors.New("plus aucune adresse IP tunnel disponible")
	ErrTunnelIPInvalid       = errors.New("réseau tunnel invalide")
)

// TunnelIPPool attribue une adresse IP unique à chaque session VPN.
type TunnelIPPool struct {
	mu        sync.Mutex
	network   *net.IPNet
	available map[string]struct{}
	allocated map[string]string // IP -> clientID
	byClient  map[string]string // clientID -> IP
}

// NewTunnelIPPool crée un pool à partir d'un réseau CIDR.
// Exemple : 10.77.0.0/24.
//
// La première adresse utilisable est réservée au serveur :
// 10.77.0.1.
//
// Les adresses attribuables commencent donc à 10.77.0.2.
func NewTunnelIPPool(cidr string) (*TunnelIPPool, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, ErrTunnelIPInvalid
	}

	ip = ip.To4()
	if ip == nil {
		return nil, ErrTunnelIPInvalid
	}

	network.IP = ip

	pool := &TunnelIPPool{
		network:   network,
		available: make(map[string]struct{}),
		allocated: make(map[string]string),
		byClient:  make(map[string]string),
	}

	// Parcours de toutes les adresses du réseau.
	for current := cloneIP(network.IP); network.Contains(current); incIP(current) {
		// .0 = adresse réseau
		if current.Equal(network.IP) {
			continue
		}

		// Dernière adresse = broadcast IPv4.
		if isBroadcast(network, current) {
			continue
		}

		// .1 = adresse du serveur TUN.
		if current.Equal(network.IP) || current[3] == 1 {
			continue
		}

		pool.available[current.String()] = struct{}{}
	}

	return pool, nil
}

// Allocate attribue une IP au client.
// Si le client possède déjà une IP, celle-ci est conservée.
func (p *TunnelIPPool) Allocate(clientID string) (net.IP, error) {
	if p == nil {
		return nil, ErrTunnelIPPoolExhausted
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if ipString, ok := p.byClient[clientID]; ok {
		return net.ParseIP(ipString).To4(), nil
	}

	for ipString := range p.available {
		delete(p.available, ipString)
		p.allocated[ipString] = clientID
		p.byClient[clientID] = ipString

		return net.ParseIP(ipString).To4(), nil
	}

	return nil, ErrTunnelIPPoolExhausted
}

// Release libère l'adresse associée au client.
func (p *TunnelIPPool) Release(clientID string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	ipString, ok := p.byClient[clientID]
	if !ok {
		return
	}

	delete(p.byClient, clientID)
	delete(p.allocated, ipString)
	p.available[ipString] = struct{}{}
}

// Lookup retourne le clientID associé à une IP tunnel.
func (p *TunnelIPPool) Lookup(ip net.IP) (string, bool) {
	if p == nil {
		return "", false
	}

	ip = ip.To4()
	if ip == nil {
		return "", false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	clientID, ok := p.allocated[ip.String()]
	return clientID, ok
}

// IP retourne l'adresse IP du client.
func (p *TunnelIPPool) IP(clientID string) (net.IP, bool) {
	if p == nil {
		return nil, false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	ipString, ok := p.byClient[clientID]
	if !ok {
		return nil, false
	}

	return net.ParseIP(ipString).To4(), true
}

// Count retourne le nombre d'adresses actuellement attribuées.
func (p *TunnelIPPool) Count() int {
	if p == nil {
		return 0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.allocated)
}

func cloneIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(network *net.IPNet, ip net.IP) bool {
	mask := network.Mask
	broadcast := make(net.IP, net.IPv4len)

	for i := 0; i < net.IPv4len; i++ {
		broadcast[i] = network.IP[i] | ^mask[i]
	}

	return ip.Equal(broadcast)
}
