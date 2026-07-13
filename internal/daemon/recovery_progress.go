package daemon

import (
	"sort"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/backup"
)

type recoveryResourceResult struct {
	ResourceKind      string
	ResourceID        string
	GenerationID      string
	SourceCompletedAt int64
	Empty             bool
}

type recoveryProgress struct {
	mutex     sync.Mutex
	completed map[string]recoveryResourceResult
	lastError string
	retry     chan struct{}
}

func newRecoveryProgress() *recoveryProgress {
	return &recoveryProgress{
		completed: make(map[string]recoveryResourceResult), retry: make(chan struct{}, 1),
	}
}

func (progress *recoveryProgress) satisfied(kind, resourceID string) bool {
	progress.mutex.Lock()
	_, exists := progress.completed[recoveryResourceKey(kind, resourceID)]
	progress.mutex.Unlock()
	return exists
}

func (progress *recoveryProgress) markLatest(
	kind, resourceID string,
	completion backup.ResourceCompletion,
	found bool,
) {
	result := recoveryResourceResult{ResourceKind: kind, ResourceID: resourceID, Empty: !found}
	if found {
		result.GenerationID = completion.GenerationID
		result.SourceCompletedAt = completion.CompletedAtMillis
	}
	progress.mark(result)
}

func (progress *recoveryProgress) markManual(request backup.ResourceRestoreRequest) {
	if request.Options.Mode != "replace" || !request.Options.DestructiveConfirmed ||
		request.Options.NewResourceName != "" {
		return
	}
	progress.mark(recoveryResourceResult{
		ResourceKind: request.ResourceKind, ResourceID: request.ResourceID,
		GenerationID:      request.GenerationID,
		SourceCompletedAt: request.Source.Completion.CompletedAtMillis,
	})
}

func (progress *recoveryProgress) mark(result recoveryResourceResult) {
	progress.mutex.Lock()
	progress.completed[recoveryResourceKey(result.ResourceKind, result.ResourceID)] = result
	progress.mutex.Unlock()
}

func (progress *recoveryProgress) results() []recoveryResourceResult {
	progress.mutex.Lock()
	result := make([]recoveryResourceResult, 0, len(progress.completed))
	for _, resource := range progress.completed {
		result = append(result, resource)
	}
	progress.mutex.Unlock()
	sort.Slice(result, func(left, right int) bool {
		if result[left].ResourceKind != result[right].ResourceKind {
			return result[left].ResourceKind < result[right].ResourceKind
		}
		return result[left].ResourceID < result[right].ResourceID
	})
	return result
}

func (progress *recoveryProgress) beginAttempt() {
	progress.mutex.Lock()
	progress.lastError = ""
	progress.mutex.Unlock()
}

func (progress *recoveryProgress) recordFailure(err error) {
	message := strings.ToValidUTF8(err.Error(), "�")
	if len(message) > 4096 {
		message = strings.ToValidUTF8(message[:4093], "") + "..."
	}
	progress.mutex.Lock()
	progress.lastError = message
	progress.mutex.Unlock()
}

func (progress *recoveryProgress) failure() string {
	progress.mutex.Lock()
	message := progress.lastError
	progress.mutex.Unlock()
	return message
}

func (progress *recoveryProgress) requestRetry() {
	select {
	case progress.retry <- struct{}{}:
	default:
	}
}

func recoveryResourceKey(kind, resourceID string) string {
	return kind + "\x00" + resourceID
}
