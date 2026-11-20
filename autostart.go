package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	unitName = "go-litemon.service"
	unitPath = "/etc/systemd/system/" + unitName
)

type AutoStatus struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Unit      string `json:"unit"`
	UnitPath  string `json:"unitPath"`
	Message   string `json:"message,omitempty"`
}

// AutostartStatus 读 systemd 状态，供面板按钮显示
func AutostartStatus() AutoStatus {
	st := AutoStatus{Unit: unitName, UnitPath: unitPath}
	if _, err := exec.LookPath("systemctl"); err != nil {
		st.Message = "本机没有 systemd，无法管理自启"
		return st
	}
	st.Supported = true
	out, _ := exec.Command("systemctl", "is-enabled", unitName).Output()
	st.Enabled = strings.TrimSpace(string(out)) == "enabled"
	out2, _ := exec.Command("systemctl", "is-active", unitName).Output()
	st.Running = strings.TrimSpace(string(out2)) == "active"
	return st
}

// SetAutostart 写/删 unit 并 enable/disable
func SetAutostart(enable bool) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("本机没有 systemd，无法设置自启")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("需要 root 权限写入 %s，请用 sudo 启动 go-litemon", unitPath)
	}
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.Abs(exe); err != nil {
			return err
		}
		unit := fmt.Sprintf(`[Unit]
Description=go-litemon 轻量性能监视器
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s --listen 0.0.0.0:9527
Restart=always
RestartSec=3
Nice=5

[Install]
WantedBy=multi-user.target
`, exe)
		if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
			return err
		}
		if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("daemon-reload: %v %s", err, out)
		}
		if out, err := exec.Command("systemctl", "enable", "--now", unitName).CombinedOutput(); err != nil {
			return fmt.Errorf("enable: %v %s", err, out)
		}
		return nil
	}
	// 关闭自启：disable + 删除 unit（不影响当前以命令行方式运行的进程）
	_, _ = exec.Command("systemctl", "disable", "--now", unitName).CombinedOutput()
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = exec.Command("systemctl", "daemon-reload").CombinedOutput()
	return nil
}
