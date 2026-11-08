//go:build linux

package gateway

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readCPUTimes() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range fields[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return idle, total, true
}

// collectHost fills CPU, memory and disk from /proc and statfs.
func collectHost(n *nodeInfo) {
	if i1, t1, ok := readCPUTimes(); ok {
		time.Sleep(200 * time.Millisecond)
		if i2, t2, ok2 := readCPUTimes(); ok2 && t2 > t1 {
			n.CPU["usage_percent"] = round1(100 * (1 - float64(i2-i1)/float64(t2-t1)))
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			n.CPU["load1"], n.CPU["load5"], n.CPU["load15"] = atof(f[0]), atof(f[1]), atof(f[2])
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		vals := map[string]uint64{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Fields(sc.Text())
			if len(p) >= 2 {
				v, _ := strconv.ParseUint(p[1], 10, 64)
				vals[strings.TrimSuffix(p[0], ":")] = v * 1024
			}
		}
		total, avail := vals["MemTotal"], vals["MemAvailable"]
		n.Memory["total_bytes"], n.Memory["available_bytes"], n.Memory["used_bytes"] = total, avail, total-avail
		if total > 0 {
			n.Memory["usage_percent"] = round1(100 * float64(total-avail) / float64(total))
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		total := st.Blocks * uint64(st.Bsize)
		free := st.Bavail * uint64(st.Bsize)
		n.Disk["path"], n.Disk["total_bytes"], n.Disk["free_bytes"], n.Disk["used_bytes"] = "/", total, free, total-free
		if total > 0 {
			n.Disk["usage_percent"] = round1(100 * float64(total-free) / float64(total))
		}
	}
}

func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
