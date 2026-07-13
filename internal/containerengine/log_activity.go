package containerengine

import (
	"path/filepath"
	"sync"
)

type logActivity struct {
	mu   sync.Mutex
	byID map[string]string
}

func (activity *logActivity) set(containerID, logPath string) {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if activity.byID == nil {
		activity.byID = make(map[string]string)
	}
	activity.byID[containerID] = filepath.Clean(logPath)
}

func (activity *logActivity) remove(containerID string) {
	activity.mu.Lock()
	delete(activity.byID, containerID)
	activity.mu.Unlock()
}

// ActiveLogPaths returns a snapshot of base log paths that may still be open
// by conmon. A stale entry is safe because it only delays deletion.
func (e *Engine) ActiveLogPaths() map[string]struct{} {
	e.logs.mu.Lock()
	defer e.logs.mu.Unlock()
	result := make(map[string]struct{}, len(e.logs.byID))
	for _, path := range e.logs.byID {
		result[path] = struct{}{}
	}
	return result
}
