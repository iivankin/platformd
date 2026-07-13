package volume

import (
	"strconv"
	"strings"
)

const maximumOwnerID = uint64(1<<32 - 2)

func parseNumericImageUser(value string) (int, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, false
	}
	uid, uidErr := strconv.ParseUint(parts[0], 10, 32)
	gid, gidErr := strconv.ParseUint(parts[1], 10, 32)
	if uidErr != nil || gidErr != nil || uid > maximumOwnerID || gid > maximumOwnerID {
		return 0, 0, false
	}
	return int(uid), int(gid), true
}
