package containerengine

import "testing"

func TestShouldNotifyImageStorageAfterPull(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		previousID  string
		pulledID    string
		wantNotify  bool
	}{
		{name: "cache hit", previousID: "abc", pulledID: "abc", wantNotify: false},
		{name: "first pull", previousID: "", pulledID: "abc", wantNotify: true},
		{name: "digest changed", previousID: "abc", pulledID: "def", wantNotify: true},
		{name: "empty pull", previousID: "", pulledID: "", wantNotify: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldNotifyImageStorageAfterPull(test.previousID, test.pulledID); got != test.wantNotify {
				t.Fatalf("shouldNotifyImageStorageAfterPull(%q, %q) = %v, want %v",
					test.previousID, test.pulledID, got, test.wantNotify)
			}
		})
	}
}

func TestShouldNotifyImageStorageAfterGC(t *testing.T) {
	t.Parallel()
	if shouldNotifyImageStorageAfterGC(ImageGarbageCollectResult{Skipped: 12}) {
		t.Fatal("empty GC should not notify")
	}
	if !shouldNotifyImageStorageAfterGC(ImageGarbageCollectResult{BuildCacheImagesRemoved: 1}) {
		t.Fatal("GC that removes build cache should notify")
	}
	if !shouldNotifyImageStorageAfterGC(ImageGarbageCollectResult{OrphanLayersRemoved: 2}) {
		t.Fatal("GC that removes orphan layers should notify")
	}
}
