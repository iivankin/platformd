//go:build linux

package cgrouptree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseUnifiedPath(t *testing.T) {
	value, err := parseUnifiedPath("0::/system.slice/platformd.service/control\n")
	if err != nil || value != "/system.slice/platformd.service/control" {
		t.Fatalf("parse unified path = %q, %v", value, err)
	}
	for _, invalid := range []string{"", "0::relative\n", "1:cpu:/value\n", "0::/a/../b\n"} {
		if _, err := parseUnifiedPath(invalid); err == nil {
			t.Fatalf("expected %q to fail", invalid)
		}
	}
}

func TestTreeCreatesKillsAndRemovesWorkloadLeaf(t *testing.T) {
	mountRoot := t.TempDir()
	workloadRoot := filepath.Join(mountRoot, "unit", workloadsLeaf)
	if err := os.MkdirAll(workloadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	tree := &Tree{mountRoot: mountRoot, workloadPath: "/unit/" + workloadsLeaf}
	leaf, err := tree.CreateLeaf("exec-id")
	if err != nil {
		t.Fatal(err)
	}
	if leaf.FD() == 0 {
		t.Fatal("cgroup directory FD is invalid")
	}
	if err := os.WriteFile(filepath.Join(workloadRoot, "exec-id", "cgroup.procs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := leaf.Kill(); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(filepath.Join(workloadRoot, "exec-id", "cgroup.kill")); err != nil || string(value) != "1\n" {
		t.Fatalf("cgroup.kill = %q, %v", value, err)
	}
	if err := leaf.file.Close(); err != nil {
		t.Fatal(err)
	}
	leaf.file = nil
	if err := os.Remove(filepath.Join(workloadRoot, "exec-id", "cgroup.kill")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workloadRoot, "exec-id", "cgroup.procs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workloadRoot, "exec-id")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workloadRoot, "exec-id")); !os.IsNotExist(err) {
		t.Fatalf("cgroup leaf remained: %v", err)
	}
}

func TestTreeParentStaysBelowDelegatedUnit(t *testing.T) {
	tree := &Tree{workloadPath: "/system.slice/platformd.service/workloads"}
	parent, err := tree.Parent("018f-resource")
	if err != nil || parent != "/system.slice/platformd.service/workloads/018f-resource" {
		t.Fatalf("parent = %q, %v", parent, err)
	}
	for _, invalid := range []string{"", "../escape", "nested/value", "white space", "ünicode", ".", ".."} {
		if _, err := tree.Parent(invalid); err == nil {
			t.Fatalf("expected resource ID %q to fail", invalid)
		}
	}
}

func TestRemoveEmptyCgroupTreeRejectsPopulatedLeaf(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workloads")
	leaf := filepath.Join(root, "resource")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, current := range []struct {
		path  string
		value string
	}{
		{filepath.Join(root, "cgroup.procs"), ""},
		{filepath.Join(root, "cgroup.threads"), ""},
		{filepath.Join(leaf, "cgroup.procs"), "42\n"},
		{filepath.Join(leaf, "cgroup.threads"), "42\n"},
	} {
		if err := os.WriteFile(current.path, []byte(current.value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeEmptyCgroupTree(root); err == nil {
		t.Fatal("expected populated workload leaf to fail")
	}
}
