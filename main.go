package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:9527", "监听地址，0.0.0.0:9527 供局域网访问")
	interval := flag.Duration("interval", 2*time.Second, "采集间隔")
	flag.Parse()

	c := NewCollector()
	c.Prime() // 先采一次，网络/磁盘速率才有基线

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go c.Run(ctx, *interval)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.Snapshot())
	})
	mux.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, AutostartStatus())
		case http.MethodPost:
			var body struct {
				Enable bool `json:"enable"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := SetAutostart(body.Enable); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "status": AutostartStatus()})
				return
			}
			writeJSON(w, map[string]any{"ok": true, "status": AutostartStatus()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	dist, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", spa(dist))

	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("go-litemon listening on http://%s  (采样 %s)", *listen, *interval)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Println("bye")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// spa 提供静态文件，非文件路径回退 index.html
func spa(dist fs.FS) http.Handler {
	index, _ := fs.ReadFile(dist, "index.html")
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

var _ = fmt.Sprintf
