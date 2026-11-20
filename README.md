# go-litemon

轻量级 Linux 性能监视面板 —— 单个 Go 二进制，**透明背景**，可直接当桌面挂件 / OBS 浏览器源。

![panel](https://img.shields.io/badge/panel-transparent-4ade80) ![go](https://img.shields.io/badge/go-1.22+-00ADD8) ![cgo](https://img.shields.io/badge/cgo-none-success)

## 功能

| 指标 | 说明 |
|---|---|
| CPU 使用率 / 负载 | gopsutil 采样，2s 一跳 |
| CPU 温度 | `/sys/class/thermal` → `hwmon` 兜底；无传感器显示 `N/A` |
| 内存 | 使用率 + used/total |
| GPU 使用率 / 温度 / 显存 | 优先 `nvidia-smi`，其次 AMD `gpu_busy_percent`，都没有显示 `N/A` |
| 磁盘读写速率 | `/proc/diskstats` 增量，KB/s → 自动进位 |
| 网络上行 / 下行 | 非回环网卡合计，B/s |
| 磁盘容量 | 每个真实挂载点一行：**进度条 + 百分比 + used/total**，等宽数字自动右对齐 |
| 自启开关 | 面板右上角按钮，一键 `systemctl enable/disable` |

## 快速开始

```bash
# 编译（无 cgo，产物可直接拷到别的机器）
make build            # 或 CGO_ENABLED=0 go build -o go-litemon .

# 运行（默认只监听回环）
./go-litemon --listen 0.0.0.0:9527

# 浏览器打开
http://<本机IP>:9527/
```

参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `--listen` | `127.0.0.1:9527` | 监听地址，`0.0.0.0:9527` 供局域网访问 |
| `--interval` | `2s` | 采集间隔 |

## 透明面板

- 页面 `html/body` 就是 `background: transparent`，默认**完全透明**，只有文字和进度条叠在你的桌面/视频上；
- 想要一层毛玻璃底：`http://<ip>:9527/?panel=0.45`（0–1 之间，越大越不透明）；
- 作为 OBS「浏览器源」添加即可，宽高随窗口自适应（`min(760px, 100%)`）。

## 自启按钮

面板右上角按钮直接调 `POST /api/autostart {"enable":true|false}`：

- 开启：写 `/etc/systemd/system/go-litemon.service`（`ExecStart=<当前二进制绝对路径> --listen 0.0.0.0:9527`）→ `daemon-reload` → `enable --now`；
- 关闭：`disable --now` → 删除 unit → `daemon-reload`；
- **需要 root**：非 root 运行时按钮会返回明确错误（`需要 root 权限写入 /etc/systemd/system/...`）。

也可以用 API 直接控制：

```bash
curl -s http://127.0.0.1:9527/api/autostart
curl -s -X POST http://127.0.0.1:9527/api/autostart -H 'Content-Type: application/json' -d '{"enable":true}'
```

## API

`GET /api/metrics`

```json
{
  "ts": 1795191820, "host": "cloudrive2",
  "cpu": 46.3, "cpuTemp": null, "mem": 14.9, "memUsed": 10018947072, "memTotal": 67141197824,
  "load1": 6.67,
  "gpuName": "N/A", "gpu": null, "gpuTemp": null, "gpuMem": null,
  "readBps": 0, "writeBps": 379.5,
  "upBps": 1406642, "downBps": 1609455,
  "disks": [
    {"mount": "/", "fsType": "xfs", "used": 64542474240, "total": 81353875456, "percent": 79.3},
    {"mount": "/boot", "fsType": "xfs", "used": 120274944, "total": 204111872, "percent": 58.9}
  ]
}
```

`GET /api/autostart` / `POST /api/autostart`

## 设计要点

- **零 cgo**：`CGO_ENABLED=0` 编出 5.4MB 静态二进制，扔到任何 Linux 都能跑；
- **速率靠增量**：网络/磁盘速率都用两次采样的差值除以真实间隔，首帧不输出假数据；
- **磁盘过滤**：只统计 ext4/xfs/btrfs/zfs/vfat/ntfs/nfs/cifs 等真实块设备，跳过 `/proc /sys /dev /run /var/lib/docker` 这类噪音；
- **对齐靠 CSS 网格**：磁盘行固定四列 `70px / 1fr / 52px / 132px`，数字统一 `font-variant-numeric: tabular-nums`，容量列右对齐——挂载点名字多长都不会错位。

## 目录

```
go-litemon/
├── main.go          # HTTP 服务、路由、静态文件
├── collect.go       # 指标采集（gopsutil + /sys + nvidia-smi）
├── autostart.go     # systemd 自启开关
├── web/index.html   # 透明面板（原生 JS，无构建）
├── Makefile
└── go.mod
```

## License

MIT
