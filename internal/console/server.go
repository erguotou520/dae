/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package console

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/daeuniverse/dae/control"
	"github.com/sirupsen/logrus"
)

//go:embed ui.html
var uiFS embed.FS

const DefaultServiceName = "dae"

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateToken() string {
	b := make([]byte, 36)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(tokenAlphabet))))
		b[i] = tokenAlphabet[n.Int64()]
	}
	return string(b)
}

type Provider interface {
	DashboardSnapshot() control.DashboardSnapshot
}

type Console struct {
	addr        string
	serviceName string
	configPath  string
	configEnv   string

	store *LogStore
	page  []byte

	token    string

	mu       sync.RWMutex
	provider Provider
	server   *http.Server
	stdOnce  sync.Once
}

func New(addr, serviceName, configPath, configEnv string) (*Console, error) {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	page, err := uiFS.ReadFile("ui.html")
	if err != nil {
		return nil, err
	}
	return &Console{
		addr:        addr,
		serviceName: serviceName,
		configPath:  configPath,
		configEnv:   configEnv,
		store:       NewLogStore(4000, 1200),
		page:        page,
		token:       generateToken(),
	}, nil
}

func (c *Console) AttachLogger(log *logrus.Logger) {
	if log != nil {
		log.AddHook(c.store)
	}
}

func (c *Console) AttachStandardLogger(log *logrus.Logger) {
	c.stdOnce.Do(func() {
		if log != nil {
			log.AddHook(c.store)
		}
	})
}

func (c *Console) RecordFlow(record control.DashboardFlowRecord) {
	c.store.RecordFlow(record)
}

func (c *Console) RecordDNS(record control.DashboardDNSRecord) {
	c.store.RecordDNS(record)
}

func (c *Console) SetProvider(p Provider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = p
}

func (c *Console) providerSnapshot() (control.DashboardSnapshot, bool) {
	c.mu.RLock()
	p := c.provider
	c.mu.RUnlock()
	if p == nil {
		return control.DashboardSnapshot{}, false
	}
	return p.DashboardSnapshot(), true
}

func (c *Console) Start(ctx context.Context) error {
	if c.addr == "" {
		return nil
	}
	logrus.Infof("[Console] Access token: %s", c.token)
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleIndex)
	mux.HandleFunc("/api/status", c.handleStatus)
	mux.HandleFunc("/api/summary", c.handleSummary)
	mux.HandleFunc("/api/groups", c.handleGroups)
	mux.HandleFunc("/api/nodes", c.handleNodes)
	mux.HandleFunc("/api/subscriptions", c.handleSubscriptions)
	mux.HandleFunc("/api/traffic", c.handleTraffic)
	mux.HandleFunc("/api/connections", c.handleConnections)
	mux.HandleFunc("/api/dns", c.handleDNS)
	mux.HandleFunc("/api/logs", c.handleLogs)
	mux.HandleFunc("/api/config", c.handleConfig)
	mux.HandleFunc("/api/reload", c.handleReload)

	srv := &http.Server{
		Addr:              c.addr,
		Handler:           c.withToken(c.withCORS(mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	c.server = srv
	ln, err := net.Listen("tcp", c.addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Warn("console server stopped")
		}
	}()
	return nil
}

func (c *Console) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Console) withToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+c.token {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Console) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(c.page)
}

func (c *Console) handleStatus(w http.ResponseWriter, r *http.Request) {
	systemd := querySystemd(c.serviceName)
	snapshot, ok := c.providerSnapshot()
	status := "offline"
	if systemd.ActiveState == "active" {
		status = "online"
		if !ok {
			status = "degraded"
		}
	} else if systemd.ActiveState == "reloading" {
		status = "reloading"
	}
	c.renderJSON(w, map[string]any{
		"status":             status,
		"systemd":            systemd,
		"activity_age":       c.store.ActivityAgeSeconds(),
		"updated_at":         time.Now(),
		"has_snapshot":       ok,
		"groups":             snapshot.TotalGroups,
		"subscription_nodes": snapshot.TotalSubscriptionNodes,
		"config_path":        func() string { p, _ := c.configPathWithHint(); return p }(),
		"config_env_key":     c.configEnv,
	})
}

func (c *Console) handleSummary(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := c.providerSnapshot()
	if !ok {
		c.renderJSON(w, map[string]any{"groups": []any{}, "nodes": []any{}, "total_groups": 0, "total_nodes": 0})
		return
	}
	c.renderJSON(w, map[string]any{
		"groups":       snapshot.Groups,
		"nodes":        snapshot.Nodes,
		"total_groups": snapshot.TotalGroups,
		"total_nodes":  snapshot.TotalSubscriptionNodes,
		"updated_at":   snapshot.UpdatedAt,
	})
}

func (c *Console) handleGroups(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := c.providerSnapshot()
	if !ok {
		c.renderJSON(w, map[string]any{"groups": []any{}})
		return
	}
	c.renderJSON(w, map[string]any{"groups": snapshot.Groups})
}

func (c *Console) handleNodes(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := c.providerSnapshot()
	if !ok {
		c.renderJSON(w, map[string]any{"nodes": []any{}})
		return
	}
	c.renderJSON(w, map[string]any{"nodes": snapshot.Nodes})
}

func (c *Console) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := c.providerSnapshot()
	if !ok {
		c.renderJSON(w, map[string]any{"subscriptions": []any{}})
		return
	}

	// Group nodes by subscription tag
	subMap := make(map[string][]control.DashboardNodeSnapshot)
	directNodes := []control.DashboardNodeSnapshot{}

	for _, node := range snapshot.Nodes {
		if node.SubscriptionTag == "" {
			directNodes = append(directNodes, node)
		} else {
			subMap[node.SubscriptionTag] = append(subMap[node.SubscriptionTag], node)
		}
	}

	subscriptions := make([]map[string]any, 0, len(subMap))
	for tag, nodes := range subMap {
		subscriptions = append(subscriptions, map[string]any{
			"tag":        tag,
			"node_count": len(nodes),
			"nodes":      nodes,
		})
	}

	// Sort subscriptions by tag name
	sort.SliceStable(subscriptions, func(i, j int) bool {
		return subscriptions[i]["tag"].(string) < subscriptions[j]["tag"].(string)
	})

	c.renderJSON(w, map[string]any{
		"subscriptions": subscriptions,
		"direct_nodes":  directNodes,
		"total_subs":    len(subscriptions),
		"total_direct":  len(directNodes),
	})
}

func (c *Console) handleTraffic(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit", 200)
	c.renderJSON(w, map[string]any{"records": c.store.Flows(limit, q)})
}

func (c *Console) handleConnections(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit", 200)
	c.renderJSON(w, map[string]any{"connections": c.store.Flows(limit, q)})
}

func (c *Console) handleDNS(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit", 200)
	c.renderJSON(w, map[string]any{"queries": c.store.DNS(limit, q)})
}

func (c *Console) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 200)
	c.renderJSON(w, map[string]any{"logs": c.store.Entries(limit)})
}

func (c *Console) handleConfig(w http.ResponseWriter, r *http.Request) {
	path, hint := c.configPathWithHint()
	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(path)
		if err != nil {
			c.renderJSON(w, map[string]any{
				"error":   err.Error(),
				"path":    path,
				"env_key": c.configEnv,
				"hint":    hint,
			})
			return
		}
		stat, err := os.Stat(path)
		meta := map[string]any{"path": path, "env_key": c.configEnv, "content": string(content), "hint": hint}
		if err == nil {
			meta["mtime"] = stat.ModTime()
			meta["size"] = stat.Size()
		}
		c.renderJSON(w, meta)
	case http.MethodPut:
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			c.renderJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": path, "hint": hint})
			return
		}
		tmp, err := os.CreateTemp(dir, ".dae-config-*")
		if err != nil {
			c.renderJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": path, "hint": hint})
			return
		}
		tmpName := tmp.Name()
		if _, err := tmp.WriteString(payload.Content); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			c.renderJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": path, "hint": hint})
			return
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			c.renderJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": path, "hint": hint})
			return
		}
		if err := os.Rename(tmpName, path); err != nil {
			_ = os.Remove(tmpName)
			c.renderJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": path, "hint": hint})
			return
		}
		c.renderJSON(w, map[string]any{"ok": true, "path": path, "hint": hint})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (c *Console) handleReload(w http.ResponseWriter, r *http.Request) {
	path, _ := c.configPathWithHint()
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	vctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, verr := exec.CommandContext(vctx, exe, "validate", "--config", path).CombinedOutput()
	if verr != nil {
		c.renderJSON(w, map[string]any{
			"ok":     false,
			"error":  "配置验证失败",
			"detail": strings.TrimSpace(string(out)),
		})
		return
	}
	if err := reloadSelf(); err != nil {
		c.renderJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	c.renderJSON(w, map[string]any{"ok": true})
}

func (c *Console) renderJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func queryInt(r *http.Request, key string, fallback int) int {
	if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func (c *Console) configPathWithHint() (string, string) {
	if env := strings.TrimSpace(os.Getenv(c.configEnv)); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, fmt.Sprintf("已使用环境变量 %s", c.configEnv)
		}
		return env, fmt.Sprintf("环境变量 %s 指向的配置文件不存在", c.configEnv)
	}
	if c.configPath != "" {
		if _, err := os.Stat(c.configPath); err == nil {
			return c.configPath, "使用启动参数中的配置文件路径"
		}
		return c.configPath, fmt.Sprintf("未找到配置文件，可设置环境变量 %s", c.configEnv)
	}
	return "/usr/local/etc/dae/config.dae", fmt.Sprintf("未配置路径，可设置环境变量 %s", c.configEnv)
}

type systemdStatus struct {
	ActiveState    string `json:"active_state"`
	SubState       string `json:"sub_state"`
	MainPID        int    `json:"main_pid"`
	ExecMainStatus string `json:"exec_main_status"`
	Result         string `json:"result"`
	UnitFileState  string `json:"unit_file_state"`
}

func querySystemd(service string) systemdStatus {
	if service == "" {
		service = DefaultServiceName
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "show", service,
		"--no-page",
		"-p", "ActiveState",
		"-p", "SubState",
		"-p", "MainPID",
		"-p", "ExecMainStatus",
		"-p", "Result",
		"-p", "UnitFileState",
	)
	out, err := cmd.Output()
	if err != nil {
		return systemdStatus{}
	}
	status := systemdStatus{}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		kv := bytes.SplitN(line, []byte{'='}, 2)
		if len(kv) != 2 {
			continue
		}
		key := string(kv[0])
		val := string(kv[1])
		switch key {
		case "ActiveState":
			status.ActiveState = val
		case "SubState":
			status.SubState = val
		case "MainPID":
			if n, err := strconv.Atoi(val); err == nil {
				status.MainPID = n
			}
		case "ExecMainStatus":
			status.ExecMainStatus = val
		case "Result":
			status.Result = val
		case "UnitFileState":
			status.UnitFileState = val
		}
	}
	return status
}

func reloadSelf() error {
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		return err
	}
	return nil
}
