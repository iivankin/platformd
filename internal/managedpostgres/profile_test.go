package managedpostgres

import "testing"

func TestStorageProfileFollowsOfficialImageLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tag               string
		volumeDestination string
		pgData            string
	}{
		{tag: "17.10-alpine3.23", volumeDestination: "/var/lib/postgresql/data", pgData: "/var/lib/postgresql/data/pgdata"},
		{tag: "18", volumeDestination: "/var/lib/postgresql", pgData: "/var/lib/postgresql/18/docker"},
		{tag: "18.4-alpine3.23", volumeDestination: "/var/lib/postgresql", pgData: "/var/lib/postgresql/18/docker"},
		{tag: "19beta1", volumeDestination: "/var/lib/postgresql", pgData: "/var/lib/postgresql/19/docker"},
	}
	for _, test := range tests {
		t.Run(test.tag, func(t *testing.T) {
			t.Parallel()
			profile := storageProfileForTag(test.tag)
			if profile.volumeDestination != test.volumeDestination || profile.pgData != test.pgData {
				t.Fatalf("storage profile for %q = %+v", test.tag, profile)
			}
		})
	}
}
