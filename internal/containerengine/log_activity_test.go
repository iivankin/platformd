package containerengine

import "testing"

func TestActiveLogPathsReturnsAnIsolatedSnapshot(t *testing.T) {
	engine := &Engine{}
	engine.logs.set("one", "/logs/one.log")
	engine.logs.set("two", "/logs/two.log")

	paths := engine.ActiveLogPaths()
	delete(paths, "/logs/one.log")
	engine.logs.remove("two")

	paths = engine.ActiveLogPaths()
	if _, exists := paths["/logs/one.log"]; !exists || len(paths) != 1 {
		t.Fatalf("active log paths = %+v", paths)
	}
}
