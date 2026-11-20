package main

/*
#cgo pkg-config: Qt5Widgets
#include <stdlib.h>
#include "widget.h"
*/
import "C"

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

func main() {
	listen := flag.String("interval", "2s", "采集间隔")
	x := flag.Int("x", -1, "窗口左上角 X（-1 = 右上角）")
	y := flag.Int("y", 40, "窗口左上角 Y")
	w := flag.Int("w", 620, "窗口宽度（像素）")
	h := flag.Int("h", 176, "窗口高度（像素）")
	shot := flag.String("screenshot", "", "离屏渲染一张 PNG 后退出（无桌面环境自检用）")
	dump := flag.Bool("dump", false, "打印一帧指标 JSON 后退出（自检用）")
	flag.Parse()

	interval, err := time.ParseDuration(*listen)
	if err != nil {
		log.Fatalf("interval 解析失败: %v", err)
	}

	// 自检模式：完全不碰 Qt（无桌面环境也能跑）
	if *dump {
		c0 := NewCollector()
		c0.Prime()
		time.Sleep(1200 * time.Millisecond)
		c0.CollectNow()
		fmt.Println(c0.Snapshot().JSON())
		return
	}

	// Qt 要求 QApplication 在任何 widget 之前创建，且在 GUI 线程（这里是主 goroutine）
	argv := []string{"go-litemon"}
	cArgv := make([]*C.char, len(argv))
	for i, s := range argv {
		cArgv[i] = C.CString(s)
		defer C.free(unsafe.Pointer(cArgv[i]))
	}
	C.lm_app_init(C.int(len(argv)), (**C.char)(unsafe.Pointer(&cArgv[0])))

	px := *x
	if px < 0 { // 默认贴右上角
		if scr := screenWidth(); scr > 0 {
			px = scr - *w - 40
		} else {
			px = 900
		}
	}
	panel := C.lm_create(C.int(px), C.int(*y), C.int(*w), C.int(*h))

	c := NewCollector()
	c.Prime()

	// 先把第一帧推上去
	push(panel, c.Snapshot())

	// 后台持续采集
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			push(panel, c.Snapshot())
		}
	}()

	// 离屏截图模式：给一帧时间渲染，然后出图退出
	if *shot != "" {
		time.Sleep(1500 * time.Millisecond) // 等一次真实采样（网络/磁盘速率才有值）
		c.CollectNow()
		pushSync(panel, c.Snapshot())
		time.Sleep(200 * time.Millisecond)
		cs := C.CString(*shot)
		defer C.free(unsafe.Pointer(cs))
		rc := C.lm_screenshot(panel, cs)
		if rc != 0 {
			fmt.Fprintf(os.Stderr, "截图失败 rc=%d\n", int(rc))
			os.Exit(1)
		}
		fmt.Println("截图已保存:", *shot)
		return
	}

	// GUI 主循环（阻塞）
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		os.Exit(0)
	}()
	C.lm_run(panel, 1, (**C.char)(unsafe.Pointer(&cArgv[0])))
}

// push 把一帧指标交给 Qt 绘制
func push(panel unsafe.Pointer, m Metrics) {
	var lm C.LMMetrics
	copyHost(&lm, m.Host)
	lm.cpu = C.double(m.CPU)
	lm.mem = C.double(m.Mem)
	lm.load1 = C.double(m.Load1)
	lm.up_bps = C.double(m.UpBps)
	lm.down_bps = C.double(m.DownBps)
	lm.read_kbps = C.double(m.ReadBps)
	lm.write_kbps = C.double(m.WriteBps)
	lm.disk_pct = C.double(m.DiskPercent)
	lm.disk_used_gb = C.double(float64(m.DiskUsed) / 1024 / 1024 / 1024)
	lm.disk_total_gb = C.double(float64(m.DiskTotal) / 1024 / 1024 / 1024)
	if m.GPU != nil {
		lm.gpu = C.double(*m.GPU)
	} else {
		lm.gpu = -1
	}
	if m.GPUMemPct != nil {
		lm.gpumem = C.double(*m.GPUMemPct)
	} else {
		lm.gpumem = -1
	}
	C.lm_update(panel, &lm)
}

// pushSync 同步推一帧并立即重绘（仅 GUI 线程/截图模式用）
func pushSync(panel unsafe.Pointer, m Metrics) {
	var lm C.LMMetrics
	copyHost(&lm, m.Host)
	lm.cpu = C.double(m.CPU)
	lm.mem = C.double(m.Mem)
	lm.load1 = C.double(m.Load1)
	lm.up_bps = C.double(m.UpBps)
	lm.down_bps = C.double(m.DownBps)
	lm.read_kbps = C.double(m.ReadBps)
	lm.write_kbps = C.double(m.WriteBps)
	lm.disk_pct = C.double(m.DiskPercent)
	lm.disk_used_gb = C.double(float64(m.DiskUsed) / 1024 / 1024 / 1024)
	lm.disk_total_gb = C.double(float64(m.DiskTotal) / 1024 / 1024 / 1024)
	if m.GPU != nil {
		lm.gpu = C.double(*m.GPU)
	} else {
		lm.gpu = -1
	}
	if m.GPUMemPct != nil {
		lm.gpumem = C.double(*m.GPUMemPct)
	} else {
		lm.gpumem = -1
	}
	C.lm_update_sync(panel, &lm)
}

// copyHost 把主机名写进定长 char[128]，避免 Go/C 之间传指针悬空
func copyHost(lm *C.LMMetrics, host string) {
	b := []byte(host)
	if len(b) > 127 {
		b = b[:127]
	}
	dst := (*[128]byte)(unsafe.Pointer(&lm.host))[0:128]
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, b)
}

// screenWidth 从 X11 环境变量估算屏宽，拿不到就返回 0
func screenWidth() int {
	// DISPLAY 存在时交给 Qt 摆位更准，这里只在无 DISPLAY 时兜底
	if os.Getenv("DISPLAY") == "" {
		return 0
	}
	return 1920
}
