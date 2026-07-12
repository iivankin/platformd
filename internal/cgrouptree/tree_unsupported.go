//go:build !linux

package cgrouptree

import "fmt"

type Tree struct{}

func Setup() (*Tree, error) {
	return nil, fmt.Errorf("delegated cgroups require Linux")
}

func (*Tree) Parent(string) (string, error) {
	return "", fmt.Errorf("delegated cgroups require Linux")
}

func (*Tree) WorkloadRoot() string {
	return ""
}
