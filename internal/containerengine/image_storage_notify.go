package containerengine

// shouldNotifyImageStorageAfterPull reports whether a completed pull changed the
// local image store. Cache hits used by auto-update polls keep the same image ID
// and must not invalidate disk-usage scans.
func shouldNotifyImageStorageAfterPull(previousID, pulledID string) bool {
	return pulledID != "" && pulledID != previousID
}

func shouldNotifyImageStorageAfterGC(result ImageGarbageCollectResult) bool {
	return result.FinalImagesRemoved+result.BuildCacheImagesRemoved+result.OrphanLayersRemoved > 0
}
