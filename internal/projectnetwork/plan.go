package projectnetwork

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

var defaultPool = netip.MustParsePrefix("10.80.0.0/12")

var ErrPoolExhausted = errors.New("project network pool is exhausted")

const (
	ContainerLeaseFirstHost = 2
	ContainerLeaseLastHost  = 191
	GatewayFirstHost        = 192
	GatewayLastHost         = 254
)

type Project struct {
	ID   string
	Name string
}

type Assignment struct {
	ProjectID   string
	ProjectName string
	NetworkName string
	Bridge      string
	Subnet      netip.Prefix
	Gateway     netip.Addr
}

type Failure struct {
	ProjectID string
	Err       error
}

type Result struct {
	Assignments []Assignment
	Failures    []Failure
}

func Plan(projects []Project, occupied []netip.Prefix) (Result, error) {
	return planWithPool(projects, occupied, defaultPool)
}

func planWithPool(projects []Project, occupied []netip.Prefix, pool netip.Prefix) (Result, error) {
	if !pool.IsValid() || !pool.Addr().Is4() || !pool.Addr().IsPrivate() || pool.Bits() > 24 {
		return Result{}, fmt.Errorf("invalid private IPv4 project pool %s", pool)
	}
	canonicalProjects := slices.Clone(projects)
	slices.SortFunc(canonicalProjects, func(left, right Project) int {
		return strings.Compare(left.ID, right.ID)
	})
	for index, project := range canonicalProjects {
		if project.ID == "" || project.Name == "" {
			return Result{}, errors.New("project ID and name are required for network planning")
		}
		if index > 0 && canonicalProjects[index-1].ID == project.ID {
			return Result{}, fmt.Errorf("duplicate project ID %q", project.ID)
		}
	}
	blocked := make([]netip.Prefix, 0, len(occupied)+len(projects))
	for _, prefix := range occupied {
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			return Result{}, fmt.Errorf("invalid occupied IPv4 prefix %s", prefix)
		}
		if prefix.Bits() == 0 {
			continue
		}
		blocked = append(blocked, prefix.Masked())
	}

	result := Result{}
	usedBridges := make(map[string]string, len(canonicalProjects))
	candidateIndex := uint32(0)
	candidateCount := uint32(1) << uint32(24-pool.Bits())
	for _, project := range canonicalProjects {
		bridge := BridgeName(project.ID)
		if owner, exists := usedBridges[bridge]; exists {
			result.Failures = append(result.Failures, Failure{
				ProjectID: project.ID,
				Err:       fmt.Errorf("bridge name collision with project %s", owner),
			})
			continue
		}
		usedBridges[bridge] = project.ID

		var subnet netip.Prefix
		for candidateIndex < candidateCount {
			candidate := subnetAt(pool, candidateIndex)
			candidateIndex++
			if !overlapsAny(candidate, blocked) {
				subnet = candidate
				break
			}
		}
		if !subnet.IsValid() {
			result.Failures = append(result.Failures, Failure{ProjectID: project.ID, Err: ErrPoolExhausted})
			continue
		}
		blocked = append(blocked, subnet)
		result.Assignments = append(result.Assignments, Assignment{
			ProjectID: project.ID, ProjectName: project.Name,
			NetworkName: bridge, Bridge: bridge, Subnet: subnet,
			Gateway: subnet.Addr().Next(),
		})
	}
	return result, nil
}

func BridgeName(projectID string) string {
	digest := sha256.Sum256([]byte(projectID))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])
	return "pd" + strings.ToLower(encoded[:13])
}

func subnetAt(pool netip.Prefix, index uint32) netip.Prefix {
	address := pool.Masked().Addr().As4()
	value := uint32(address[0])<<24 | uint32(address[1])<<16 | uint32(address[2])<<8 | uint32(address[3])
	value += index << 8
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}), 24)
}

func HostAddress(subnet netip.Prefix, host int) (netip.Addr, error) {
	if !subnet.IsValid() || !subnet.Addr().Is4() || subnet.Bits() != 24 || host < 1 || host > 254 {
		return netip.Addr{}, fmt.Errorf("invalid /24 host address request %s host %d", subnet, host)
	}
	bytes := subnet.Masked().Addr().As4()
	bytes[3] = byte(host)
	return netip.AddrFrom4(bytes), nil
}

func overlapsAny(candidate netip.Prefix, blocked []netip.Prefix) bool {
	for _, prefix := range blocked {
		if candidate.Contains(prefix.Addr()) || prefix.Contains(candidate.Addr()) {
			return true
		}
	}
	return false
}
