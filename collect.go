package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type Disk struct {
	Mount   string  `json:"mount"`
	FsType  string  `json:"fsType"`
	Used    uint64  `json:"used"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

type Metrics struct {
	TS        int64    `json:"ts"`
	Host      string   `json:"host"`
	CPU       float64  `json:"cpu"`
	CPUTemp   *float64 `json:"cpuTemp"`
	Mem       float64  `json:"mem"`
	MemUsed   uint64   `json:"memUsed"`
	MemTotal  uint64   `json:"memTotal"`
	Load1     float64  `json:"load1"`
	GPUName   string   `json:"gpuName"`
	GPU       *float64 `json:"gpu"`
	GPUTemp   *float64 `json:"gpuTemp"`
	GPUMemPct *float64 `json:"gpuMem"`
	ReadBps   float64  `json:"readBps"`
	WriteBps  float64  `json:"writeBps"`
	UpBps     float64  `json:"upBps"`
	DownBps   float64  `json:"downBps"`
	// 磁盘总容量（按设备去重后汇总，1 个块设备只算一次）
	DiskUsed    uint64  `json:"diskUsed"`
	DiskTotal   uint64  `json:"diskTotal"`
	DiskPercent float64 `json:"diskPercent"`
	Disks       []Disk  `json:"disks"`
}

// 只统计真实块设备，过滤虚拟文件系统
var realFs = map[string]bool{
	"ext4": true, "ext3": true, "xfs": true, "btrfs": true, "zfs": true,
	"vfat": true, "exfat": true, "fuseblk": true, "ntfs": true, "ntfs3": true,
	"nfs": true, "nfs4": true, "cifs": true, "f2fs": true, "overlay": false,
}

var skipMountPrefix = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker",
	"/var/lib/kubelet", "/var/lib/containerd", "/boot/efi",
}

type Collector struct {
	mu        sync.RWMutex
	cur       Metrics
	prevSent  uint64
	prevRecv  uint64
	prevRead  uint64
	prevWrite uint64
	prevT     time.Time
}

func NewCollector() *Collector { return &Collector{cur: Metrics{Disks: []Disk{}}} }

// Prime 采一次基线，避免第一帧速率是天文数字
func (c *Collector) Prime() {
	c.collect(0)
}

func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.collect(interval)
		}
	}
}

func (c *Collector) Snapshot() Metrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cur
}

func (c *Collector) collect(interval time.Duration) {
	now := time.Now()
	m := Metrics{TS: now.Unix(), Disks: []Disk{}}

	if h, err := host.Info(); err == nil {
		m.Host = h.Hostname
	}
	if p, err := cpu.Percent(0, false); err == nil && len(p) > 0 {
		m.CPU = round1(p[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.Mem = round1(vm.UsedPercent)
		m.MemUsed = vm.Used
		m.MemTotal = vm.Total
	}
	if l := readLoad1(); l >= 0 {
		m.Load1 = round2(l)
	}
	m.CPUTemp = readCPUTemp()

	// 网络速率（所有非回环网卡合计）
	if cs, err := net.IOCounters(false); err == nil && len(cs) > 0 {
		var sent, recv uint64
		for _, n := range cs {
			sent += n.BytesSent
			recv += n.BytesRecv
		}
		if interval > 0 && (c.prevSent > 0 || c.prevRecv > 0) && sent >= c.prevSent && recv >= c.prevRecv {
			m.UpBps = round1(float64(sent-c.prevSent) / interval.Seconds())
			m.DownBps = round1(float64(recv-c.prevRecv) / interval.Seconds())
		}
		c.prevSent, c.prevRecv = sent, recv
	}

	// 磁盘 IO 速率
	if io, err := disk.IOCounters(); err == nil {
		var rb, wb uint64
		for _, v := range io {
			rb += v.ReadBytes
			wb += v.WriteBytes
		}
		if interval > 0 && (c.prevRead > 0 || c.prevWrite > 0) && rb >= c.prevRead && wb >= c.prevWrite {
			m.ReadBps = round1(float64(rb-c.prevRead) / 1024 / interval.Seconds())
			m.WriteBps = round1(float64(wb-c.prevWrite) / 1024 / interval.Seconds())
		}
		c.prevRead, c.prevWrite = rb, wb
	}

	// 磁盘容量
	if parts, err := disk.Partitions(false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if !isRealFs(p.Fstype) || skipMount(p.Mountpoint) || seen[p.Device] {
				continue
			}
			u, err := disk.Usage(p.Mountpoint)
			if err != nil || u.Total == 0 {
				continue
			}
			seen[p.Device] = true
			m.Disks = append(m.Disks, Disk{
				Mount: p.Mountpoint, FsType: p.Fstype,
				Used: u.Used, Total: u.Total, Percent: round1(u.UsedPercent),
			})
		}
	}
	// 汇总（已按 device 去重，不会把同一块盘的多个挂载点重复累加）
	for _, d := range m.Disks {
		m.DiskUsed += d.Used
		m.DiskTotal += d.Total
	}
	if m.DiskTotal > 0 {
		m.DiskPercent = round1(float64(m.DiskUsed) / float64(m.DiskTotal) * 100)
	}

	sort.Slice(m.Disks, func(i, j int) bool {
		if m.Disks[i].Mount == "/" {
			return true
		}
		if m.Disks[j].Mount == "/" {
			return false
		}
		return m.Disks[i].Mount < m.Disks[j].Mount
	})

	// GPU（有 nvidia-smi 用 nvidia-smi，有 amdgpu sysfs 用 sysfs，否则 N/A）
	if g, ok := readNvidia(); ok {
		m.GPUName = g.name
		m.GPU = g.util
		m.GPUTemp = g.temp
		m.GPUMemPct = g.memPct
	} else if g, ok := readAMDGPU(); ok {
		m.GPUName = g.name
		m.GPU = g.util
		m.GPUTemp = g.temp
	} else {
		m.GPUName = gpuNameFromDRM()
	}

	c.mu.Lock()
	c.cur = m
	c.mu.Unlock()
	c.prevT = now
}

func isRealFs(fs string) bool {
	v, ok := realFs[fs]
	return ok && v
}

func skipMount(p string) bool {
	for _, s := range skipMountPrefix {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

func readLoad1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return -1
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return -1
	}
	return v
}

// 从 /sys/class/thermal 里取最高温度（CPU 封装通常是最高的那个）
func readCPUTemp() *float64 {
	dirs, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil || len(dirs) == 0 {
		return nil
	}
	var best float64
	found := false
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "temp"))
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil || v <= 0 {
			continue
		}
		c := v
		if c > 1000 { // 毫度
			c = c / 1000
		}
		if !found || c > best {
			best, found = c, true
		}
	}
	if !found {
		// 兜底：hwmon（coretemp / k10temp / zenpower）
		if hw, err := filepath.Glob("/sys/class/hwmon/hwmon*/temp*_input"); err == nil {
			for _, f := range hw {
				b, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				v := parseF(strings.TrimSpace(string(b))) / 1000
				if v <= 0 || v > 150 {
					continue
				}
				if !found || v > best {
					best, found = v, true
				}
			}
		}
	}
	if !found {
		return nil
	}
	r := round1(best)
	return &r
}

type gpuStat struct {
	name   string
	util   *float64
	temp   *float64
	memPct *float64
}

func readNvidia() (gpuStat, bool) {
	bin, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return gpuStat{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"--query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuStat{}, false
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	parts := strings.Split(line, ",")
	if len(parts) < 5 {
		return gpuStat{}, false
	}
	u := round1(parseF(parts[1]))
	t := round1(parseF(parts[2]))
	used, total := parseF(parts[3]), parseF(parts[4])
	var mp *float64
	if total > 0 {
		v := round1(used / total * 100)
		mp = &v
	}
	return gpuStat{name: strings.TrimSpace(parts[0]), util: &u, temp: &t, memPct: mp}, true
}

func readAMDGPU() (gpuStat, bool) {
	busy, err := os.ReadFile("/sys/class/drm/card0/device/gpu_busy_percent")
	if err != nil {
		return gpuStat{}, false
	}
	u := round1(parseF(strings.TrimSpace(string(busy))))
	g := gpuStat{name: gpuNameFromDRM(), util: &u}
	if b, err := os.ReadFile("/sys/class/drm/card0/device/hwmon/hwmon0/temp1_input"); err == nil {
		t := round1(parseF(strings.TrimSpace(string(b))) / 1000)
		g.temp = &t
	}
	return g, true
}

func gpuNameFromDRM() string {
	dirs, _ := filepath.Glob("/sys/class/drm/card[0-9]")
	if len(dirs) == 0 {
		return "N/A"
	}
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, "device", "vendor")); err == nil {
			switch strings.TrimSpace(string(b)) {
			case "0x10de":
				return "NVIDIA"
			case "0x1002":
				return "AMD"
			case "0x8086":
				return "Intel"
			}
		}
	}
	return "N/A"
}

func parseF(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
