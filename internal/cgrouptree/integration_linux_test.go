//go:build linux && integration

package cgrouptree

import (
	"os"
	"path"
	"testing"
)

func TestSetupInsideDelegatedSystemdUnit(t *testing.T) {
	if os.Getenv("PLATFORMD_CGROUP_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_CGROUP_INTEGRATION=1 inside an isolated delegated systemd unit")
	}
	tree, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	if path.Base(tree.WorkloadRoot()) != workloadsLeaf {
		t.Fatalf("unexpected workload root %q", tree.WorkloadRoot())
	}
	parent, err := tree.Parent("integration-resource")
	if err != nil || path.Dir(parent) != tree.WorkloadRoot() {
		t.Fatalf("unexpected workload parent %q: %v", parent, err)
	}
	second, err := Setup()
	if err != nil {
		t.Fatalf("repeat delegated cgroup setup: %v", err)
	}
	if second.WorkloadRoot() != tree.WorkloadRoot() {
		t.Fatalf("repeat setup changed workload root from %q to %q", tree.WorkloadRoot(), second.WorkloadRoot())
	}
}
