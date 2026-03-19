/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package dialer

import (
	"sort"
	"time"
)

type DashboardDialerSnapshot struct {
	Name            string `json:"name"`
	SubscriptionTag string `json:"subscription_tag"`
	Alive           bool   `json:"alive"`
	LatencyMS       int64  `json:"latency_ms"`
	MovingAverageMS int64  `json:"moving_average_ms"`
}

type DashboardAliveSetSnapshot struct {
	NetworkType     string                    `json:"network_type"`
	SelectionPolicy string                    `json:"selection_policy"`
	BestDialer      string                    `json:"best_dialer"`
	BestLatencyMS   int64                     `json:"best_latency_ms"`
	Dialers         []DashboardDialerSnapshot `json:"dialers"`
}

// DashboardSnapshot exports a stable snapshot for console/UI usage.
func (d *Dialer) DashboardSnapshot(typ *NetworkType) DashboardDialerSnapshot {
	col := d.mustGetCollection(typ)
	latency := time.Duration(0)
	if last, ok := col.Latencies10.LastLatency(); ok {
		latency = last
	}
	return DashboardDialerSnapshot{
		Name:            d.property.Name,
		SubscriptionTag: d.property.SubscriptionTag,
		Alive:           col.Alive,
		LatencyMS:       latency.Milliseconds(),
		MovingAverageMS: col.MovingAverage.Milliseconds(),
	}
}

// DashboardSnapshot exports the current alive dialer set state.
func (a *AliveDialerSet) DashboardSnapshot() DashboardAliveSetSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	snap := DashboardAliveSetSnapshot{
		NetworkType:     a.CheckTyp.String(),
		SelectionPolicy: string(a.selectionPolicy),
		BestLatencyMS:   a.minLatency.sortingLatency.Milliseconds(),
	}
	if a.minLatency.dialer != nil {
		snap.BestDialer = a.minLatency.dialer.Property().Name
	}

	alive := make([]struct {
		d *Dialer
		l time.Duration
		o time.Duration
	}, 0, len(a.inorderedAliveDialerSet))
	for _, d := range a.inorderedAliveDialerSet {
		latency, ok := a.dialerToLatency[d]
		if !ok {
			continue
		}
		alive = append(alive, struct {
			d *Dialer
			l time.Duration
			o time.Duration
		}{d: d, l: latency, o: a.dialerToLatencyOffset[d]})
	}
	sort.SliceStable(alive, func(i, j int) bool {
		return alive[i].l+alive[i].o < alive[j].l+alive[j].o
	})
	snap.Dialers = make([]DashboardDialerSnapshot, 0, len(alive))
	for _, item := range alive {
		col := item.d.mustGetCollection(a.CheckTyp)
		snap.Dialers = append(snap.Dialers, DashboardDialerSnapshot{
			Name:            item.d.property.Name,
			SubscriptionTag: item.d.property.SubscriptionTag,
			Alive:           col.Alive,
			LatencyMS:       item.l.Milliseconds(),
			MovingAverageMS: col.MovingAverage.Milliseconds(),
		})
	}
	return snap
}
