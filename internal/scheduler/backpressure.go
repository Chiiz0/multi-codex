package scheduler

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

const DefaultRetryAfterSeconds = 10

type Backpressure struct {
	Executor          string      `json:"executor"`
	RetryAfterSeconds int         `json:"retry_after_seconds"`
	AvailableSlots    int64       `json:"available_slots"`
	Nodes             []NodeState `json:"nodes"`
}

type NodeState struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Status           string  `json:"status"`
	Eligible         bool    `json:"eligible"`
	IneligibleReason string  `json:"ineligible_reason,omitempty"`
	ActiveRuns       int64   `json:"active_runs"`
	Concurrency      int64   `json:"concurrency"`
	AvailableSlots   int64   `json:"available_slots"`
	Utilization      float64 `json:"utilization"`
	SelectionRank    int     `json:"selection_rank,omitempty"`
	SelectionReason  string  `json:"selection_reason,omitempty"`
}

func Snapshot(st store.Store, executor string) Backpressure {
	activeByNode := map[string]int64{}
	for _, run := range st.ListAllRuns() {
		if !runIsActive(run.Status) || run.ExecutorNodeID == "" {
			continue
		}
		activeByNode[run.ExecutorNodeID]++
	}
	snapshot := Backpressure{
		Executor:          executor,
		RetryAfterSeconds: DefaultRetryAfterSeconds,
		Nodes:             []NodeState{},
	}
	for _, node := range st.ListExecutorNodes() {
		if node.Kind != executor {
			continue
		}
		state := NodeState{
			ID:          node.ID,
			Name:        node.Name,
			Status:      node.Status,
			ActiveRuns:  activeByNode[node.ID],
			Concurrency: concurrencyForNode(node),
			Eligible:    true,
		}
		state.Utilization = utilizationForNode(state.ActiveRuns, state.Concurrency)
		if node.Status != "active" {
			state.Eligible = false
			state.IneligibleReason = "node_not_active"
		} else if executor == "ssh" && !node.HostKeyVerified {
			state.Eligible = false
			state.IneligibleReason = "host_key_unverified"
		} else if state.ActiveRuns >= state.Concurrency {
			state.Eligible = false
			state.IneligibleReason = "capacity_full"
		} else {
			state.AvailableSlots = state.Concurrency - state.ActiveRuns
			state.SelectionReason = "eligible_capacity"
			snapshot.AvailableSlots += state.AvailableSlots
		}
		snapshot.Nodes = append(snapshot.Nodes, state)
	}
	sort.SliceStable(snapshot.Nodes, func(i, j int) bool {
		left := snapshot.Nodes[i]
		right := snapshot.Nodes[j]
		if left.Eligible != right.Eligible {
			return left.Eligible
		}
		if left.Utilization != right.Utilization {
			return left.Utilization < right.Utilization
		}
		if left.AvailableSlots != right.AvailableSlots {
			return left.AvailableSlots > right.AvailableSlots
		}
		return left.Name < right.Name
	})
	rank := 1
	for idx := range snapshot.Nodes {
		if !snapshot.Nodes[idx].Eligible {
			if snapshot.Nodes[idx].SelectionReason == "" {
				snapshot.Nodes[idx].SelectionReason = snapshot.Nodes[idx].IneligibleReason
			}
			continue
		}
		snapshot.Nodes[idx].SelectionRank = rank
		rank++
	}
	return snapshot
}

func concurrencyForNode(node domain.ExecutorNode) int64 {
	value, ok := node.Capacity["concurrency"]
	if !ok {
		return 1
	}
	switch typed := value.(type) {
	case int:
		return atLeastOne(int64(typed))
	case int64:
		return atLeastOne(typed)
	case float64:
		return atLeastOne(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return atLeastOne(parsed)
		}
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return atLeastOne(parsed)
		}
	}
	return 1
}

func atLeastOne(value int64) int64 {
	if value < 1 {
		return 1
	}
	return value
}

func utilizationForNode(active int64, concurrency int64) float64 {
	concurrency = atLeastOne(concurrency)
	value := float64(active) / float64(concurrency)
	return math.Round(value*10000) / 10000
}

func runIsActive(status string) bool {
	switch status {
	case "queued", "preparing", "running":
		return true
	default:
		return false
	}
}
