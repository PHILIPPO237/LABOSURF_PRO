//go:build !linux || android

package main

type NetworkConfig struct {
	TUNName    string
	TUNAddress string
	VPNRange   string
	MTU        int
}

func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		TUNName:    "labosurf0",
		TUNAddress: "10.77.0.1/24",
		VPNRange:   "10.77.0.0/24",
		MTU:        1500,
	}
}

func ConfigureNetwork(cfg NetworkConfig) (cleanup func(), err error) {
	return func() {}, nil
}
func CleanupNetwork() {}
