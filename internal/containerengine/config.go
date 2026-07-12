package containerengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnsupported = errors.New("container runtime is supported only on Linux/amd64 with cgo")

type Config struct {
	TransientRoot     string
	RunRoot           string
	GraphRoot         string
	LogRoot           string
	StaticDir         string
	VolumePath        string
	NetworkConfigDir  string
	HooksDir          string
	CDISpecDir        string
	ContainersConf    string
	StorageConf       string
	RegistriesConf    string
	SignaturePolicy   string
	SeccompProfile    string
	DefaultMountsFile string
	OCIRuntime        string
	Conmon            string
	AllowedMountRoots []string
}

func (c Config) Validate() error {
	paths := map[string]string{
		"transient root":      c.TransientRoot,
		"run root":            c.RunRoot,
		"graph root":          c.GraphRoot,
		"log root":            c.LogRoot,
		"static dir":          c.StaticDir,
		"volume path":         c.VolumePath,
		"network config dir":  c.NetworkConfigDir,
		"hooks dir":           c.HooksDir,
		"CDI spec dir":        c.CDISpecDir,
		"containers.conf":     c.ContainersConf,
		"storage.conf":        c.StorageConf,
		"registries.conf":     c.RegistriesConf,
		"signature policy":    c.SignaturePolicy,
		"seccomp profile":     c.SeccompProfile,
		"default mounts file": c.DefaultMountsFile,
		"OCI runtime":         c.OCIRuntime,
		"conmon":              c.Conmon,
	}
	for name, path := range paths {
		if err := validateAbsolutePath(name, path); err != nil {
			return err
		}
	}
	for _, root := range c.AllowedMountRoots {
		if err := validateAbsolutePath("allowed mount root", root); err != nil {
			return err
		}
	}
	if c.TransientRoot == string(filepath.Separator) {
		return errors.New("transient root cannot be the filesystem root")
	}
	for name, path := range map[string]string{
		"run root":           c.RunRoot,
		"static dir":         c.StaticDir,
		"volume path":        c.VolumePath,
		"network config dir": c.NetworkConfigDir,
		"hooks dir":          c.HooksDir,
		"CDI spec dir":       c.CDISpecDir,
	} {
		if !pathWithin(path, c.TransientRoot) {
			return fmt.Errorf("%s %q must be below transient root %q", name, path, c.TransientRoot)
		}
	}
	for name, path := range map[string]string{"graph root": c.GraphRoot, "log root": c.LogRoot} {
		if path == c.TransientRoot || pathWithin(path, c.TransientRoot) {
			return fmt.Errorf("%s %q cannot be below transient root %q", name, path, c.TransientRoot)
		}
	}
	return nil
}

func validateAbsolutePath(name, path string) error {
	if path == "" {
		return fmt.Errorf("%s path is empty", name)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s path %q is not canonical and absolute", name, path)
	}
	return nil
}

func requireRegularFile(path string, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
