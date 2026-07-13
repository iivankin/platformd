//go:build !linux

package cgrouptree

import (
	"context"
	"fmt"
)

type Tree struct{}
type Leaf struct{}

func Setup() (*Tree, error) {
	return nil, fmt.Errorf("delegated cgroups require Linux")
}

func (*Tree) Parent(string) (string, error) {
	return "", fmt.Errorf("delegated cgroups require Linux")
}

func (*Tree) WorkloadRoot() string {
	return ""
}

func (*Tree) SetFrozen(context.Context, bool) error {
	return fmt.Errorf("delegated cgroups require Linux")
}

func (*Tree) CreateLeaf(string) (*Leaf, error) {
	return nil, fmt.Errorf("delegated cgroups require Linux")
}

func (*Leaf) FD() uintptr { return 0 }

func (*Leaf) Kill() error { return fmt.Errorf("delegated cgroups require Linux") }

func (*Leaf) Close(context.Context) error { return fmt.Errorf("delegated cgroups require Linux") }
