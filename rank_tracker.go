package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RankEntry records the current state of one rank slot in a CephFS filesystem.
// AssignedAt is set (or reset) whenever the incarnation counter changes for
// this (rank, fs_id) pair — the authoritative signal that a new daemon has
// taken the rank (failover or restart into the same slot).
type RankEntry struct {
	Rank        int    `json:"rank"`
	FsID        string `json:"fs_id"`
	Daemon      string `json:"daemon"`      // daemon currently holding this rank
	Incarnation int    `json:"incarnation"` // Ceph incarnation counter; 0 = standby-replay
	AssignedAt  int64  `json:"assigned_at"` // unix timestamp of last incarnation change
}

// rankStoreKey identifies a unique rank slot.
// IsSR differentiates the standby-replay shadow from the active holder of the same rank.
type rankStoreKey struct {
	FsID string
	Rank int
	IsSR bool
}

type rankStore struct {
	mu sync.RWMutex
	// Internal storage keyed by rank slot, not daemon name.
	// cluster → (fs_id, rank) → RankEntry
	data     map[string]map[rankStoreKey]RankEntry
	filePath string
	// seenIncarnation tracks whether we've received ceph_mds_rank_incarnation
	// data for a cluster; used to detect undeployed textfile script.
	seenIncarnation map[string]bool
}

var rankTracker = &rankStore{
	data:            make(map[string]map[rankStoreKey]RankEntry),
	seenIncarnation: make(map[string]bool),
}

// reIncarnation matches ceph_mds_rank_incarnation lines from the aggregated
// node metrics body (emitted by ceph_mdsmap_textfile.sh on mon hosts).
// Example line:
//
//	ceph_mds_rank_incarnation{ceph_daemon="fs7.site1.mds2.xswazx",fs_id="7",fs_name="fs7",rank="0",state="up:active"} 747313
var (
	reIncDaemon = regexp.MustCompile(`[{,]ceph_daemon="([^"]+)"`)
	reIncFsID   = regexp.MustCompile(`[{,]fs_id="([^"]+)"`)
	reIncRank   = regexp.MustCompile(`[{,]rank="(-?\d+)"`)
)

// load reads persisted rank assignments from filePath. Called once at startup.
// The on-disk format is the flattened daemon→entry map (same as the API response)
// so we need to re-key it into the internal rank-keyed structure.
func (rs *rankStore) load(filePath string) {
	rs.filePath = filePath
	b, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("rank_tracker: load %s: %v", filePath, err)
		}
		return
	}

	// On-disk: { cluster: { daemon: RankEntry } }
	var raw map[string]map[string]RankEntry
	if err := json.Unmarshal(b, &raw); err != nil {
		log.Printf("rank_tracker: parse %s: %v", filePath, err)
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	total := 0
	for cluster, daemonMap := range raw {
		slotMap := make(map[rankStoreKey]RankEntry, len(daemonMap))
		for _, e := range daemonMap {
			key := rankStoreKey{FsID: e.FsID, Rank: e.Rank, IsSR: e.Incarnation == 0}
			slotMap[key] = e
			total++
		}
		rs.data[cluster] = slotMap
	}
	log.Printf("rank_tracker: loaded %d rank assignments from %s", total, filePath)
}

// updateFromNodeMetrics parses ceph_mds_rank_incarnation lines from the
// aggregated node metrics body (written by ceph_mdsmap_textfile.sh on mon
// hosts and picked up via node_exporter textfile collector). It records a new
// AssignedAt timestamp whenever:
//   - an active rank slot changes incarnation (new daemon took the rank), or
//   - a standby-replay slot changes daemon name (new daemon became the SR).
//
// SR daemons (incarnation = 0) are tracked under a separate IsSR=true key so
// their assignment time is independent of the active holder's.
func (rs *rankStore) updateFromNodeMetrics(clusterName string, body []byte) {
	// Parse all ceph_mds_rank_incarnation lines into a current snapshot.
	type incarnEntry struct {
		daemon      string
		incarnation int
	}
	current := map[rankStoreKey]incarnEntry{}

	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "ceph_mds_rank_incarnation{") {
			continue
		}
		dm := reIncDaemon.FindStringSubmatch(line)
		if dm == nil {
			continue
		}
		fm := reIncFsID.FindStringSubmatch(line)
		if fm == nil {
			continue
		}
		rm := reIncRank.FindStringSubmatch(line)
		if rm == nil {
			continue
		}
		rank, err := strconv.Atoi(rm[1])
		if err != nil || rank < 0 {
			continue
		}
		// Value is after the closing } and whitespace.
		sp := strings.LastIndex(line, "} ")
		if sp < 0 {
			continue
		}
		incVal, err := strconv.Atoi(strings.TrimSpace(line[sp+2:]))
		if err != nil {
			continue
		}
		key := rankStoreKey{FsID: fm[1], Rank: rank, IsSR: incVal == 0}
		current[key] = incarnEntry{daemon: dm[1], incarnation: incVal}
	}

	if len(current) == 0 {
		return // ceph_mdsmap_textfile.sh not yet deployed on this cluster's mons
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.seenIncarnation[clusterName] = true

	slotMap, ok := rs.data[clusterName]
	if !ok {
		slotMap = make(map[rankStoreKey]RankEntry)
		rs.data[clusterName] = slotMap
	}

	changed := false
	now := time.Now().Unix()

	for key, cur := range current {
		existing, exists := slotMap[key]
		if !exists {
			// First time seeing this slot. For SR we don't know when the current daemon
			// became SR, so use AssignedAt=0 — the API omits these and the JS falls back
			// to ceph_daemon_start_time_seconds. For active, now is the best estimate.
			assignedAt := now
			if key.IsSR {
				assignedAt = 0
			}
			slotMap[key] = RankEntry{
				Rank:        key.Rank,
				FsID:        key.FsID,
				Daemon:      cur.daemon,
				Incarnation: cur.incarnation,
				AssignedAt:  assignedAt,
			}
			kind := "rank"
			if key.IsSR {
				kind = "SR slot"
			}
			log.Printf("[%s] rank_tracker: new %s fs_id=%s rank=%d daemon=%s incarnation=%d",
				clusterName, kind, key.FsID, key.Rank, cur.daemon, cur.incarnation)
			changed = true
		} else if !key.IsSR && cur.incarnation != existing.Incarnation {
			// Active rank: incarnation changed → new daemon took the rank.
			log.Printf("[%s] rank_tracker: rank reassigned fs_id=%s rank=%d %s→%s incarnation=%d→%d",
				clusterName, key.FsID, key.Rank,
				existing.Daemon, cur.daemon,
				existing.Incarnation, cur.incarnation)
			slotMap[key] = RankEntry{
				Rank:        key.Rank,
				FsID:        key.FsID,
				Daemon:      cur.daemon,
				Incarnation: cur.incarnation,
				AssignedAt:  now,
			}
			changed = true
		} else if key.IsSR && cur.daemon != existing.Daemon {
			// SR slot: daemon name changed → new daemon became the standby-replay.
			log.Printf("[%s] rank_tracker: SR daemon changed fs_id=%s rank=%d %s→%s",
				clusterName, key.FsID, key.Rank, existing.Daemon, cur.daemon)
			slotMap[key] = RankEntry{
				Rank:        key.Rank,
				FsID:        key.FsID,
				Daemon:      cur.daemon,
				Incarnation: cur.incarnation,
				AssignedAt:  now,
			}
			changed = true
		} else if cur.daemon != existing.Daemon {
			// Same incarnation but daemon label changed (shouldn't happen normally).
			slotMap[key] = RankEntry{
				Rank:        existing.Rank,
				FsID:        existing.FsID,
				Daemon:      cur.daemon,
				Incarnation: existing.Incarnation,
				AssignedAt:  existing.AssignedAt,
			}
			changed = true
		}
	}

	// Remove rank slots that no longer appear (FS removed, rank scaled down, SR cleared).
	for key, existing := range slotMap {
		if _, ok := current[key]; !ok {
			kind := "rank"
			if key.IsSR {
				kind = "SR slot"
			}
			log.Printf("[%s] rank_tracker: %s gone fs_id=%s rank=%d (was daemon=%s incarnation=%d)",
				clusterName, kind, key.FsID, key.Rank, existing.Daemon, existing.Incarnation)
			delete(slotMap, key)
			changed = true
		}
	}

	if changed {
		rs.saveLocked()
	}
}

// saveLocked writes the current state to disk atomically.
// assignedCount returns the number of rank slots recorded for a cluster.
func (rs *rankStore) assignedCount(clusterName string) int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.data[clusterName])
}

// On-disk format: { cluster: { daemon: RankEntry } } — daemon-keyed for
// easy reload and human readability. Caller must hold rs.mu.
func (rs *rankStore) saveLocked() {
	if rs.filePath == "" {
		return
	}
	// Build daemon-keyed map for serialisation.
	out := make(map[string]map[string]RankEntry, len(rs.data))
	for cluster, slotMap := range rs.data {
		dm := make(map[string]RankEntry, len(slotMap))
		for _, e := range slotMap {
			dm[e.Daemon] = e
		}
		out[cluster] = dm
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Printf("rank_tracker: marshal: %v", err)
		return
	}
	tmp := rs.filePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("rank_tracker: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, rs.filePath); err != nil {
		log.Printf("rank_tracker: rename %s→%s: %v", tmp, rs.filePath, err)
	}
}

// rankAssignmentsHandler serves GET /rank-assignments?cluster=<name>
// Returns {ceph_daemon: {rank, fs_id, daemon, incarnation, assigned_at}} for
// all active rank holders in the requested cluster.
func rankAssignmentsHandler(w http.ResponseWriter, r *http.Request) {
	_, name, err := getClusterState(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rankTracker.mu.RLock()
	slotMap := rankTracker.data[name]
	out := make(map[string]RankEntry, len(slotMap))
	for _, e := range slotMap {
		if e.AssignedAt == 0 {
			continue // SR first-observation: assignment time unknown; JS falls back to service uptime
		}
		out[e.Daemon] = e
	}
	rankTracker.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("rank_tracker: encode response: %v", err)
	}
}
