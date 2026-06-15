// Package sysinfo gathers CPU/RAM/disk/uptime from /proc + df via the executor
// (works for local and remote Linux). Port of system.py's shell stats.
package sysinfo

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/executor"
)

func cat(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, out, _ := executor.Get().Run(ctx, []string{"cat", path}, "")
	return out
}

// Get returns system stats (zeros if /proc is unavailable, e.g. on Windows dev).
func Get() map[string]any {
	ramTotal, ramAvail := int64(0), int64(0)
	for _, line := range strings.Split(cat("/proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		key := strings.TrimSuffix(f[0], ":")
		val, _ := strconv.ParseInt(f[1], 10, 64)
		val *= 1024
		switch key {
		case "MemTotal":
			ramTotal = val
		case "MemAvailable":
			ramAvail = val
		case "MemFree":
			if ramAvail == 0 {
				ramAvail = val
			}
		}
	}
	ramUsed := ramTotal - ramAvail

	diskTotal, diskUsed := int64(0), int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	_, df, _ := executor.Get().Run(ctx, []string{"df", "-B1", "--output=size,used", "/"}, "")
	cancel()
	for _, line := range strings.Split(df, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 {
			diskTotal, _ = strconv.ParseInt(f[0], 10, 64)
			diskUsed, _ = strconv.ParseInt(f[1], 10, 64)
			break
		}
	}

	t1, i1 := cpuTimes()
	time.Sleep(500 * time.Millisecond)
	t2, i2 := cpuTimes()
	cpuPct := 0.0
	if dt := t2 - t1; dt > 0 {
		cpuPct = round1((1 - float64(i2-i1)/float64(dt)) * 100)
	}

	uptime := 0.0
	if f := strings.Fields(cat("/proc/uptime")); len(f) > 0 {
		uptime, _ = strconv.ParseFloat(f[0], 64)
	}

	return map[string]any{
		"cpu_percent":    cpuPct,
		"ram_total":      ramTotal,
		"ram_used":       ramUsed,
		"ram_percent":    pct(ramUsed, ramTotal),
		"disk_total":     diskTotal,
		"disk_used":      diskUsed,
		"disk_percent":   pct(diskUsed, diskTotal),
		"uptime_seconds": uptime,
	}
}

func cpuTimes() (total, idle int64) {
	for _, line := range strings.Split(cat("/proc/stat"), "\n") {
		if strings.HasPrefix(line, "cpu ") {
			var vals []int64
			for _, s := range strings.Fields(line)[1:] {
				v, _ := strconv.ParseInt(s, 10, 64)
				vals = append(vals, v)
			}
			if len(vals) < 4 {
				return 1, 1
			}
			idle = vals[3]
			if len(vals) > 4 {
				idle += vals[4]
			}
			for _, v := range vals {
				total += v
			}
			return total, idle
		}
	}
	return 1, 1
}

func pct(used, total int64) float64 {
	if total == 0 {
		return 0
	}
	return round1(float64(used) / float64(total) * 100)
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
