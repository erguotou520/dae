/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package console

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/daeuniverse/dae/common"
	"github.com/daeuniverse/dae/config"
	"github.com/gorilla/websocket"
)

type wsEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type wsHub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
}

func newWSHub() *wsHub {
	return &wsHub{
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (h *wsHub) add(conn *websocket.Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = struct{}{}
}

func (h *wsHub) remove(conn *websocket.Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
}

func (h *wsHub) broadcast(payload wsEnvelope) {
	if h == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			_ = conn.Close()
			delete(h.clients, conn)
		}
	}
}

type configEntrySnapshot struct {
	Source  string `json:"source"`
	Tag     string `json:"tag,omitempty"`
	Value   string `json:"value"`
	Display string `json:"display"`
}

type configSnapshot struct {
	Path          string                `json:"path"`
	EnvKey        string                `json:"env_key"`
	Hint          string                `json:"hint"`
	Error         string                `json:"error,omitempty"`
	Content       string                `json:"content,omitempty"`
	Subscriptions []configEntrySnapshot `json:"subscriptions,omitempty"`
	Nodes         []configEntrySnapshot `json:"nodes,omitempty"`
}

type initialState struct {
	Status      map[string]any `json:"status"`
	Summary     any            `json:"summary"`
	Config      configSnapshot `json:"config"`
	Logs        []LogEntry     `json:"logs"`
	Traffic     []FlowRecord   `json:"traffic"`
	Connections []FlowRecord   `json:"connections"`
	DNS         []DNSRecord    `json:"dns"`
}

type syncState struct {
	Status  map[string]any `json:"status"`
	Summary any            `json:"summary"`
	Config  configSnapshot `json:"config"`
}

func (c *Console) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != c.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	if err := conn.WriteJSON(wsEnvelope{Type: "snapshot", Data: c.bootstrapState()}); err != nil {
		return
	}

	c.hub.add(conn)
	defer c.hub.remove(conn)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *Console) bootstrapState() initialState {
	return initialState{
		Status:      c.statusPayload(),
		Summary:     c.summaryPayload(),
		Config:      c.configPayload(),
		Logs:        c.store.Entries(250),
		Traffic:     c.store.Flows(200, ""),
		Connections: c.store.Flows(200, ""),
		DNS:         c.store.DNS(200, ""),
	}
}

func (c *Console) syncPayload() syncState {
	return syncState{
		Status:  c.statusPayload(),
		Summary: c.summaryPayload(),
		Config:  c.configPayload(),
	}
}

func (c *Console) statusPayload() map[string]any {
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
	return map[string]any{
		"status":             status,
		"systemd":            systemd,
		"activity_age":       c.store.ActivityAgeSeconds(),
		"updated_at":         time.Now(),
		"has_snapshot":       ok,
		"groups":             snapshot.TotalGroups,
		"subscription_nodes": snapshot.TotalSubscriptionNodes,
		"config_path":        func() string { p, _ := c.configPathWithHint(); return p }(),
		"config_hint":        func() string { _, h := c.configPathWithHint(); return h }(),
		"config_env_key":     c.configEnv,
	}
}

func (c *Console) summaryPayload() map[string]any {
	snapshot, ok := c.providerSnapshot()
	if !ok {
		return map[string]any{"groups": []any{}, "nodes": []any{}, "total_groups": 0, "total_nodes": 0}
	}
	return map[string]any{
		"groups":       snapshot.Groups,
		"nodes":        snapshot.Nodes,
		"total_groups": snapshot.TotalGroups,
		"total_nodes":  snapshot.TotalSubscriptionNodes,
		"updated_at":   snapshot.UpdatedAt,
	}
}

func (c *Console) configPayload() configSnapshot {
	c.mu.RLock()
	if c.configCacheOnce {
		snap := c.configCache
		c.mu.RUnlock()
		return snap
	}
	c.mu.RUnlock()

	snap := c.buildConfigPayload()

	c.mu.Lock()
	c.configCache = snap
	c.configCacheOnce = true
	c.mu.Unlock()

	return snap
}

func (c *Console) buildConfigPayload() configSnapshot {
	path, hint := c.configPathWithHint()
	view := configSnapshot{
		Path:   path,
		EnvKey: c.configEnv,
		Hint:   hint,
	}
	content, err := os.ReadFile(path)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Content = string(content)

	merger := config.NewMerger(path)
	sections, _, err := merger.Merge()
	if err != nil {
		view.Error = err.Error()
		return view
	}
	conf, err := config.New(sections)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Subscriptions = makeConfigEntries("subscription", conf.Subscription)
	view.Nodes = makeConfigEntries("node", conf.Node)
	return view
}

func (c *Console) invalidateConfigCache() {
	c.mu.Lock()
	c.configCacheOnce = false
	c.mu.Unlock()
}

func makeConfigEntries(source string, values []config.KeyableString) []configEntrySnapshot {
	entries := make([]configEntrySnapshot, 0, len(values))
	for _, raw := range values {
		tag, value := common.GetTagFromLinkLikePlaintext(string(raw))
		display := value
		if tag != "" {
			display = tag + ":" + value
		}
		entries = append(entries, configEntrySnapshot{
			Source:  source,
			Tag:     tag,
			Value:   value,
			Display: display,
		})
	}
	return entries
}

func (c *Console) syncState() syncState {
	return c.syncPayload()
}

func (c *Console) broadcastEvent(typ string, payload any) {
	if c == nil || c.hub == nil {
		return
	}
	c.hub.broadcast(wsEnvelope{Type: typ, Data: payload})
}

func (c *Console) broadcastSync() {
	if c == nil || c.hub == nil {
		return
	}
	c.hub.broadcast(wsEnvelope{Type: "sync", Data: c.syncPayload()})
	c.lastSyncSig.Store(c.syncSig())
}
