package internaldns

import "strings"

// ParseInternalName splits a.{project}.internal into the resource label and project name.
func ParseInternalName(value string) (resource, project string, ok bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if !strings.HasSuffix(value, ".internal") {
		return "", "", false
	}
	rest := strings.TrimSuffix(value, ".internal")
	index := strings.LastIndexByte(rest, '.')
	if index <= 0 || index == len(rest)-1 {
		return "", "", false
	}
	resource = rest[:index]
	project = rest[index+1:]
	if resource == "" || project == "" || strings.ContainsRune(resource, '.') {
		return "", "", false
	}
	return resource, project, true
}
