package admission

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

const (
	maximumBlockers = 64
	maximumFieldLen = 256
)

var (
	ErrBusy     = errors.New("platform has active operations")
	ErrUpdating = errors.New("platform is updating")
)

type Blocker struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Snapshot struct {
	Blockers  []Blocker `json:"blockers"`
	Total     int       `json:"total"`
	Truncated bool      `json:"truncated"`
}

type Gate struct {
	mu       sync.Mutex
	next     uint64
	updating bool
	active   map[uint64]Blocker
}

type Lease struct {
	once    sync.Once
	release func()
}

func New() *Gate {
	return &Gate{active: make(map[uint64]Blocker)}
}

func (gate *Gate) Begin(kind, id string) (*Lease, error) {
	if !validField(kind) || !validField(id) {
		return nil, errors.New("admission blocker kind and ID must be bounded printable values")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.updating {
		return nil, ErrUpdating
	}
	gate.next++
	token := gate.next
	gate.active[token] = Blocker{Kind: kind, ID: id}
	return &Lease{release: func() { gate.finish(token) }}, nil
}

func (gate *Gate) TryUpdate() (*Lease, Snapshot, error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.updating {
		return nil, Snapshot{}, ErrUpdating
	}
	if len(gate.active) != 0 {
		return nil, gate.snapshotLocked(), ErrBusy
	}
	gate.updating = true
	return &Lease{release: gate.finishUpdate}, Snapshot{}, nil
}

func (gate *Gate) Snapshot() (Snapshot, bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.snapshotLocked(), gate.updating
}

func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(lease.release)
}

func (gate *Gate) finish(token uint64) {
	gate.mu.Lock()
	delete(gate.active, token)
	gate.mu.Unlock()
}

func (gate *Gate) finishUpdate() {
	gate.mu.Lock()
	gate.updating = false
	gate.mu.Unlock()
}

func (gate *Gate) snapshotLocked() Snapshot {
	blockers := make([]Blocker, 0, min(len(gate.active), maximumBlockers))
	for _, blocker := range gate.active {
		blockers = append(blockers, blocker)
	}
	sort.Slice(blockers, func(left, right int) bool {
		if blockers[left].Kind == blockers[right].Kind {
			return blockers[left].ID < blockers[right].ID
		}
		return blockers[left].Kind < blockers[right].Kind
	})
	total := len(blockers)
	if total > maximumBlockers {
		blockers = blockers[:maximumBlockers]
	}
	return Snapshot{Blockers: blockers, Total: total, Truncated: total > len(blockers)}
}

func validField(value string) bool {
	if value == "" || len(value) > maximumFieldLen || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
