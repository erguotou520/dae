/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package control

import (
	"sort"
	"time"

	"github.com/daeuniverse/dae/component/outbound"
)

type DashboardFlowRecord struct {
	Time     time.Time `json:"time"`
	Domain   string    `json:"domain"`
	Src      string    `json:"src"`
	Dst      string    `json:"dst"`
	Network  string    `json:"network"`
	Outbound string    `json:"outbound"`
	Dialer   string    `json:"dialer"`
	Policy   string    `json:"policy"`
	DNS      string    `json:"dns"`
	PID      string    `json:"pid"`
	PName    string    `json:"pname"`
	Mac      string    `json:"mac"`
}

type DashboardDNSRecord struct {
	Time     time.Time `json:"time"`
	QName    string    `json:"qname"`
	QType    string    `json:"qtype"`
	Network  string    `json:"network"`
	Outbound string    `json:"outbound"`
	Dialer   string    `json:"dialer"`
	Upstream string    `json:"upstream"`
	Answer   string    `json:"answer"`
}

type DashboardRecorder interface {
	RecordFlow(record DashboardFlowRecord)
	RecordDNS(record DashboardDNSRecord)
}

type DashboardNodeSnapshot struct {
	Name            string   `json:"name"`
	SubscriptionTag string   `json:"subscription_tag"`
	Groups          []string `json:"groups"`
	BestLatencyMS   int64    `json:"best_latency_ms"`
}

type DashboardSnapshot struct {
	UpdatedAt              time.Time                         `json:"updated_at"`
	TotalGroups            int                               `json:"total_groups"`
	TotalSubscriptionNodes int                               `json:"total_subscription_nodes"`
	Groups                 []outbound.DashboardGroupSnapshot `json:"groups"`
	Nodes                  []DashboardNodeSnapshot           `json:"nodes"`
}

func (c *ControlPlane) DashboardSnapshot() DashboardSnapshot {
	snap := DashboardSnapshot{
		UpdatedAt:              time.Now(),
		TotalSubscriptionNodes: c.totalSubscriptionNodes(),
	}

	if len(c.outbounds) > 2 {
		snap.Groups = make([]outbound.DashboardGroupSnapshot, 0, len(c.outbounds)-2)
		for _, g := range c.outbounds[2:] {
			snap.Groups = append(snap.Groups, g.Snapshot())
		}
		sort.SliceStable(snap.Groups, func(i, j int) bool {
			return snap.Groups[i].Name < snap.Groups[j].Name
		})
		snap.TotalGroups = len(snap.Groups)
	}

	nodeMap := make(map[string]*DashboardNodeSnapshot)
	for _, g := range snap.Groups {
		for _, network := range g.Networks {
			for _, dialer := range network.Dialers {
				key := dialer.SubscriptionTag + "\x00" + dialer.Name
				node, ok := nodeMap[key]
				if !ok {
					node = &DashboardNodeSnapshot{
						Name:            dialer.Name,
						SubscriptionTag: dialer.SubscriptionTag,
						Groups:          []string{},
					}
					nodeMap[key] = node
				}
				if dialer.LatencyMS > 0 && (node.BestLatencyMS == 0 || dialer.LatencyMS < node.BestLatencyMS) {
					node.BestLatencyMS = dialer.LatencyMS
				}
				if !containsString(node.Groups, g.Name) {
					node.Groups = append(node.Groups, g.Name)
				}
			}
		}
	}
	for subtag, nodes := range c.subscriptionNodes {
		for _, name := range nodes {
			key := subtag + "\x00" + name
			node, ok := nodeMap[key]
			if !ok {
				node = &DashboardNodeSnapshot{
					Name:            name,
					SubscriptionTag: subtag,
					Groups:          []string{},
				}
				nodeMap[key] = node
			}
			if node.SubscriptionTag == "" {
				node.SubscriptionTag = subtag
			}
		}
	}
	snap.Nodes = make([]DashboardNodeSnapshot, 0, len(nodeMap))
	for _, node := range nodeMap {
		sort.Strings(node.Groups)
		snap.Nodes = append(snap.Nodes, *node)
	}
	sort.SliceStable(snap.Nodes, func(i, j int) bool {
		if snap.Nodes[i].SubscriptionTag == snap.Nodes[j].SubscriptionTag {
			return snap.Nodes[i].Name < snap.Nodes[j].Name
		}
		return snap.Nodes[i].SubscriptionTag < snap.Nodes[j].SubscriptionTag
	})
	if snap.TotalGroups == 0 {
		snap.TotalGroups = len(snap.Groups)
	}
	return snap
}

func (c *ControlPlane) totalSubscriptionNodes() int {
	if len(c.subscriptionNodes) == 0 {
		return 0
	}
	seen := make(map[string]struct{})
	for _, nodes := range c.subscriptionNodes {
		for _, node := range nodes {
			seen[node] = struct{}{}
		}
	}
	return len(seen)
}

func containsString(values []string, v string) bool {
	for _, item := range values {
		if item == v {
			return true
		}
	}
	return false
}

func (c *ControlPlane) recordFlow(record DashboardFlowRecord) {
	if c == nil || c.dashboard == nil {
		return
	}
	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	c.dashboard.RecordFlow(record)
}
