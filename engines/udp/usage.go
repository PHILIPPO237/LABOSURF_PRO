package main

import "fmt"

// FormatBytes convertit un nombre d'octets en unité lisible.
func FormatBytes(bytes uint64) string {
	const unit = 1024

	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	value := float64(bytes)
	units := []string{"KB", "MB", "GB", "TB"}

	for _, u := range units {
		value /= unit

		if value < unit {
			return fmt.Sprintf("%.2f %s", value, u)
		}
	}

	return fmt.Sprintf("%.2f PB", value)
}
