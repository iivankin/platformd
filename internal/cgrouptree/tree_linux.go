//go:build linux

package cgrouptree

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	defaultMountRoot = "/sys/fs/cgroup"
	selfCgroupPath   = "/proc/self/cgroup"
	controlLeaf      = "control"
	workloadsLeaf    = "workloads"
)

var requiredControllers = []string{"cpu", "io", "memory", "pids"}

type Tree struct {
	mountRoot    string
	unitPath     string
	workloadPath string
}

func Setup() (*Tree, error) {
	value, err := os.ReadFile(selfCgroupPath)
	if err != nil {
		return nil, fmt.Errorf("read own cgroup: %w", err)
	}
	currentPath, err := parseUnifiedPath(string(value))
	if err != nil {
		return nil, err
	}
	return setupAt(defaultMountRoot, currentPath, os.Getpid())
}

func setupAt(mountRoot, currentPath string, pid int) (*Tree, error) {
	unitPath, err := delegatedUnitPath(currentPath)
	if err != nil {
		return nil, err
	}
	unitRoot := filepath.Join(mountRoot, filepath.FromSlash(strings.TrimPrefix(unitPath, "/")))
	controlRoot := filepath.Join(unitRoot, controlLeaf)
	if err := requirePID(controlRoot, pid); err != nil {
		return nil, err
	}
	if err := requireEmptyCgroup(unitRoot); err != nil {
		return nil, fmt.Errorf("delegated unit cgroup must be an empty inner node: %w", err)
	}
	if err := requireControllers(unitRoot); err != nil {
		return nil, err
	}

	workloadsRoot := filepath.Join(unitRoot, workloadsLeaf)
	if err := removeEmptyCgroupTree(workloadsRoot); err != nil {
		return nil, fmt.Errorf("reset workload cgroups: %w", err)
	}
	if err := enableControllers(unitRoot); err != nil {
		return nil, err
	}
	if err := os.Mkdir(workloadsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create workload cgroup root: %w", err)
	}
	if err := requireControllers(workloadsRoot); err != nil {
		return nil, err
	}
	if err := enableControllers(workloadsRoot); err != nil {
		return nil, err
	}

	return &Tree{
		mountRoot:    mountRoot,
		unitPath:     unitPath,
		workloadPath: path.Join(unitPath, workloadsLeaf),
	}, nil
}

func (tree *Tree) Parent(resourceID string) (string, error) {
	if !validResourceID(resourceID) {
		return "", fmt.Errorf("invalid cgroup resource ID %q", resourceID)
	}
	return path.Join(tree.workloadPath, resourceID), nil
}

func validResourceID(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func (tree *Tree) WorkloadRoot() string {
	return tree.workloadPath
}

func parseUnifiedPath(value string) (string, error) {
	var found string
	for line := range strings.Lines(value) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[0] != "0" || parts[1] != "" {
			continue
		}
		if found != "" {
			return "", errors.New("own cgroup contains multiple unified hierarchy entries")
		}
		found = parts[2]
	}
	if found == "" || !path.IsAbs(found) || path.Clean(found) != found {
		return "", errors.New("own cgroup has no canonical unified hierarchy path")
	}
	return found, nil
}

func delegatedUnitPath(currentPath string) (string, error) {
	if !path.IsAbs(currentPath) || path.Clean(currentPath) != currentPath || path.Base(currentPath) != controlLeaf {
		return "", fmt.Errorf("platformd must run in a systemd DelegateSubgroup=%s leaf, got %q", controlLeaf, currentPath)
	}
	unitPath := path.Dir(currentPath)
	if unitPath == "/" {
		return "", errors.New("delegated unit cgroup cannot be hierarchy root")
	}
	return unitPath, nil
}

func requirePID(cgroupRoot string, pid int) error {
	pids, err := readIDs(filepath.Join(cgroupRoot, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read control cgroup processes: %w", err)
	}
	if !slices.Contains(pids, pid) {
		return fmt.Errorf("main PID %d is not in delegated control cgroup", pid)
	}
	return nil
}

func requireEmptyCgroup(cgroupRoot string) error {
	pids, err := readIDs(filepath.Join(cgroupRoot, "cgroup.procs"))
	if err != nil {
		return err
	}
	if len(pids) != 0 {
		return fmt.Errorf("cgroup contains processes %v", pids)
	}
	return nil
}

func requireControllers(cgroupRoot string) error {
	value, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read cgroup controllers: %w", err)
	}
	available := strings.Fields(string(value))
	for _, controller := range requiredControllers {
		if !slices.Contains(available, controller) {
			return fmt.Errorf("delegated cgroup controller %q is unavailable", controller)
		}
	}
	return nil
}

func enableControllers(cgroupRoot string) error {
	commands := make([]string, 0, len(requiredControllers))
	for _, controller := range requiredControllers {
		commands = append(commands, "+"+controller)
	}
	controlPath := filepath.Join(cgroupRoot, "cgroup.subtree_control")
	if err := os.WriteFile(controlPath, []byte(strings.Join(commands, " ")+"\n"), 0o644); err != nil {
		return fmt.Errorf("enable delegated cgroup controllers: %w", err)
	}
	value, err := os.ReadFile(controlPath)
	if err != nil {
		return fmt.Errorf("verify delegated cgroup controllers: %w", err)
	}
	enabled := strings.Fields(string(value))
	for _, controller := range requiredControllers {
		if !slices.Contains(enabled, controller) {
			return fmt.Errorf("delegated cgroup controller %q remained disabled", controller)
		}
	}
	return nil
}

func removeEmptyCgroupTree(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := removeEmptyCgroupTree(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	if err := requireEmptyCgroup(root); err != nil {
		return err
	}
	threads, err := readIDs(filepath.Join(root, "cgroup.threads"))
	if err != nil {
		return err
	}
	if len(threads) != 0 {
		return fmt.Errorf("cgroup contains threads %v", threads)
	}
	return os.Remove(root)
}

func readIDs(filename string) ([]int, error) {
	value, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(value))
	result := make([]int, 0, len(fields))
	for _, field := range fields {
		identifier, err := strconv.Atoi(field)
		if err != nil || identifier <= 0 {
			return nil, fmt.Errorf("invalid cgroup identifier %q", field)
		}
		result = append(result, identifier)
	}
	return result, nil
}
