package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func readRAM() string {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "N/D"
	}
	var total, available uint64
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			total, _ = strconv.ParseUint(f[1], 10, 64)
		}
		if len(f) >= 2 && f[0] == "MemAvailable:" {
			available, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	if total == 0 {
		return "N/D"
	}
	used := total
	if available < total {
		used = total - available
	}
	return fmt.Sprintf("%d/%d GB", used/1024/1024, total/1024/1024)
}

func readDisk() string {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return "N/D"
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used := total - free
	return fmt.Sprintf("%d/%d GB", used/1024/1024/1024, total/1024/1024/1024)
}

func serviceActive() bool {
	err := exec.Command("systemctl", "is-active", "--quiet", "labosurf.service").Run()
	return err == nil
}

func printSystemOverview() {
	width := terminalWidth()
	if width < 48 {
		width = 48
	}
	inner := width - 1
	col := inner / 3
	if col < 16 {
		col = 16
	}
	ip := serverAddress()
	udp := "🔴 ARRÊTÉ"
	if serviceActive() {
		udp = "🟢 ACTIF"
	}
	version := engineVersion

	fmt.Println()
	fmt.Println(colorDim + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + colorReset)
	fmt.Printf("%s|%s%-*s%s|%s%-*s%s|%s%-*s%s|%s\n", colorDim, colorReset, col, " 🖥️ VPS", colorCyan, colorReset, col, " ⚙️ SYSTÈME", colorCyan, colorReset, col, " 📡 LABOSURF", colorCyan, colorDim)
	fmt.Println(colorDim + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + colorReset)
	left := []string{fmt.Sprintf("CPU : %d CORES", runtime.NumCPU()), "DISQUE : " + readDisk(), "UPTIME : voir état"}
	mid := []string{"RAM : " + readRAM(), "IP : " + ip, "VERSION : " + version}
	right := []string{udp, "PORT UDP : 5667", "LICENCE : vérifiée au démarrage"}
	for i := range left {
		fmt.Printf("%s|%s %-*s%s|%s %-*s%s|%s %-*s%s|%s\n", colorDim, colorReset, col-1, left[i], colorReset, colorReset, col-1, mid[i], colorReset, colorReset, col-1, right[i], colorReset, colorDim)
	}
	fmt.Println(colorDim + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + strings.Repeat("-", col) + "+" + colorReset)
}
