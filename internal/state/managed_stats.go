package state

type ManagedStatSample struct {
	Kind        string // postgres|redis|object_store
	ResourceID  string
	ObservedAt  int64
	MetricsJSON string
}
