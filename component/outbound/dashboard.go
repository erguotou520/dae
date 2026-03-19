/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package outbound

import (
	"github.com/daeuniverse/dae/common/consts"
	"github.com/daeuniverse/dae/component/outbound/dialer"
)

type DashboardNetworkSnapshot struct {
	NetworkType      string                           `json:"network_type"`
	SelectionPolicy  string                           `json:"selection_policy"`
	CurrentDialer    string                           `json:"current_dialer"`
	CurrentLatencyMS int64                            `json:"current_latency_ms"`
	Dialers          []dialer.DashboardDialerSnapshot `json:"dialers"`
}

type DashboardGroupSnapshot struct {
	Name     string                     `json:"name"`
	Policy   string                     `json:"policy"`
	Networks []DashboardNetworkSnapshot `json:"networks"`
}

func networkTypesForGroup() []dialer.NetworkType {
	return []dialer.NetworkType{
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_4, IsDns: false},
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_6, IsDns: false},
		{L4Proto: consts.L4ProtoStr_UDP, IpVersion: consts.IpVersionStr_4, IsDns: false},
		{L4Proto: consts.L4ProtoStr_UDP, IpVersion: consts.IpVersionStr_6, IsDns: false},
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_4, IsDns: true},
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_6, IsDns: true},
		{L4Proto: consts.L4ProtoStr_UDP, IpVersion: consts.IpVersionStr_4, IsDns: true},
		{L4Proto: consts.L4ProtoStr_UDP, IpVersion: consts.IpVersionStr_6, IsDns: true},
	}
}

func (g *DialerGroup) Snapshot() DashboardGroupSnapshot {
	snap := DashboardGroupSnapshot{
		Name:   g.Name,
		Policy: string(g.GetSelectionPolicy()),
	}
	snap.Networks = make([]DashboardNetworkSnapshot, 0, 8)
	for _, typ := range networkTypesForGroup() {
		typCopy := typ
		networkSnap := DashboardNetworkSnapshot{
			NetworkType:     typ.String(),
			SelectionPolicy: string(g.GetSelectionPolicy()),
		}
		if d, latency, err := g.Select(&typCopy, true); err == nil && d != nil {
			networkSnap.CurrentDialer = d.Property().Name
			networkSnap.CurrentLatencyMS = latency.Milliseconds()
		}
		networkSnap.Dialers = make([]dialer.DashboardDialerSnapshot, 0, len(g.Dialers))
		for _, d := range g.Dialers {
			networkSnap.Dialers = append(networkSnap.Dialers, d.DashboardSnapshot(&typCopy))
		}
		snap.Networks = append(snap.Networks, networkSnap)
	}
	return snap
}
