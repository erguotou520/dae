/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2022-2025, daeuniverse Organization <dae@v2raya.org>
 */

package console

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/daeuniverse/dae/control"
	"github.com/sirupsen/logrus"
)

type LogEntry struct {
	ID      int64             `json:"id"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

type FlowRecord = control.DashboardFlowRecord
type DNSRecord = control.DashboardDNSRecord

type LogStore struct {
	mu          sync.RWMutex
	maxEntries  int
	maxRecords  int
	nextID      int64
	entries     []LogEntry
	flows       []FlowRecord
	dnsRecords  []DNSRecord
	subscribers map[int]chan any
	nextSubID   int
	lastUpdate  time.Time
}

func NewLogStore(maxEntries, maxRecords int) *LogStore {
	if maxEntries <= 0 {
		maxEntries = 5000
	}
	if maxRecords <= 0 {
		maxRecords = 1000
	}
	return &LogStore{
		maxEntries:  maxEntries,
		maxRecords:  maxRecords,
		subscribers: make(map[int]chan any),
	}
}

func (s *LogStore) touchLocked(ts time.Time) {
	if ts.IsZero() {
		ts = time.Now()
	}
	s.lastUpdate = ts
}

func (s *LogStore) Fire(entry *logrus.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	fields := make(map[string]string, len(entry.Data))
	for k, v := range entry.Data {
		fields[k] = fmt.Sprint(v)
	}

	record := LogEntry{
		ID:      s.nextID,
		Time:    entry.Time,
		Level:   entry.Level.String(),
		Message: strings.TrimSpace(entry.Message),
		Fields:  fields,
	}
	s.entries = append(s.entries, record)
	s.touchLocked(record.Time)
	if len(s.entries) > s.maxEntries {
		s.entries = append([]LogEntry(nil), s.entries[len(s.entries)-s.maxEntries:]...)
	}

	for _, ch := range s.subscribers {
		select {
		case ch <- record:
		default:
		}
	}
	return nil
}

func (s *LogStore) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (s *LogStore) RecordFlow(record control.DashboardFlowRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	s.flows = append(s.flows, FlowRecord(record))
	s.touchLocked(record.Time)
	if len(s.flows) > s.maxRecords {
		s.flows = append([]FlowRecord(nil), s.flows[len(s.flows)-s.maxRecords:]...)
	}
}

func (s *LogStore) RecordDNS(record control.DashboardDNSRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	s.dnsRecords = append(s.dnsRecords, DNSRecord(record))
	s.touchLocked(record.Time)
	if len(s.dnsRecords) > s.maxRecords {
		s.dnsRecords = append([]DNSRecord(nil), s.dnsRecords[len(s.dnsRecords)-s.maxRecords:]...)
	}
}

func (s *LogStore) Subscribe() (<-chan any, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSubID
	s.nextSubID++
	ch := make(chan any, 32)
	s.subscribers[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(ch)
		}
	}
}

func (s *LogStore) Entries(limit int) []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := takeTail(s.entries, limit)
	reverse(values)
	return values
}

func (s *LogStore) Flows(limit int, query string) []FlowRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterFlows(append([]FlowRecord(nil), s.flows...), query, limit)
}

func (s *LogStore) DNS(limit int, query string) []DNSRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterDNS(append([]DNSRecord(nil), s.dnsRecords...), query, limit)
}

func (s *LogStore) Latest() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastUpdate
}

func (s *LogStore) ActivityAgeSeconds() int64 {
	latest := s.Latest()
	if latest.IsZero() {
		return -1
	}
	return int64(time.Since(latest).Seconds())
}

func takeTail[T any](values []T, limit int) []T {
	if limit <= 0 || len(values) <= limit {
		return append([]T(nil), values...)
	}
	return append([]T(nil), values[len(values)-limit:]...)
}

func reverse[T any](values []T) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func normalizeQuery(query string) string {
	return strings.TrimSpace(strings.ToLower(query))
}

func filterFlows(values []FlowRecord, query string, limit int) []FlowRecord {
	query = normalizeQuery(query)
	if limit <= 0 {
		limit = 100
	}
	out := make([]FlowRecord, 0, min(limit, len(values)))
	for i := len(values) - 1; i >= 0; i-- {
		v := values[i]
		if query != "" && !strings.Contains(strings.ToLower(v.Domain), query) &&
			!strings.Contains(strings.ToLower(v.Dst), query) &&
			!strings.Contains(strings.ToLower(v.Dialer), query) &&
			!strings.Contains(strings.ToLower(v.Outbound), query) &&
			!strings.Contains(strings.ToLower(v.DNS), query) {
			continue
		}
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func filterDNS(values []DNSRecord, query string, limit int) []DNSRecord {
	query = normalizeQuery(query)
	if limit <= 0 {
		limit = 100
	}
	out := make([]DNSRecord, 0, min(limit, len(values)))
	for i := len(values) - 1; i >= 0; i-- {
		v := values[i]
		if query != "" && !strings.Contains(strings.ToLower(v.QName), query) &&
			!strings.Contains(strings.ToLower(v.Dialer), query) &&
			!strings.Contains(strings.ToLower(v.Upstream), query) &&
			!strings.Contains(strings.ToLower(v.Outbound), query) {
			continue
		}
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
