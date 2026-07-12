package semver

import (
	"errors"
	"strings"
)

type Version struct {
	core       [3]string
	prerelease []string
}

func Parse(value string) (Version, error) {
	if value == "" || strings.Contains(value, "+") {
		return Version{}, errors.New("version must be strict SemVer without build metadata")
	}
	coreValue, prereleaseValue, hasPrerelease := strings.Cut(value, "-")
	coreParts := strings.Split(coreValue, ".")
	if len(coreParts) != 3 {
		return Version{}, errors.New("version core must contain major.minor.patch")
	}
	var result Version
	for index, part := range coreParts {
		if !validNumeric(part) {
			return Version{}, errors.New("version core has an invalid numeric identifier")
		}
		result.core[index] = part
	}
	if !hasPrerelease {
		return result, nil
	}
	result.prerelease = strings.Split(prereleaseValue, ".")
	for _, part := range result.prerelease {
		if !validIdentifier(part) || (numeric(part) && len(part) > 1 && part[0] == '0') {
			return Version{}, errors.New("version prerelease has an invalid identifier")
		}
	}
	return result, nil
}

func Compare(left, right Version) int {
	for index := range left.core {
		if compared := compareNumeric(left.core[index], right.core[index]); compared != 0 {
			return compared
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < min(len(left.prerelease), len(right.prerelease)); index++ {
		leftPart := left.prerelease[index]
		rightPart := right.prerelease[index]
		leftNumeric := numeric(leftPart)
		rightNumeric := numeric(rightPart)
		switch {
		case leftNumeric && rightNumeric:
			if compared := compareNumeric(leftPart, rightPart); compared != 0 {
				return compared
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case leftPart < rightPart:
			return -1
		case leftPart > rightPart:
			return 1
		}
	}
	switch {
	case len(left.prerelease) < len(right.prerelease):
		return -1
	case len(left.prerelease) > len(right.prerelease):
		return 1
	default:
		return 0
	}
}

func validNumeric(value string) bool {
	return numeric(value) && (len(value) == 1 || value[0] != '0')
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && character != '-' {
			return false
		}
	}
	return true
}

func compareNumeric(left, right string) int {
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
