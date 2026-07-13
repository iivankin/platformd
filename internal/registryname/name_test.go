package registryname

import "testing"

func TestDistributionRepositoryTagAndDigestValidation(t *testing.T) {
	for _, valid := range []string{"app", "team/api", "team/a_b.c--d"} {
		if err := ValidateRepository(valid); err != nil {
			t.Fatalf("repository %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "Team/API", "team/api:latest", "team//api"} {
		if err := ValidateRepository(invalid); err == nil {
			t.Fatalf("accepted invalid repository %q", invalid)
		}
	}
	if err := ValidateTag("release-1.2"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTag("bad/tag"); err == nil {
		t.Fatal("accepted invalid tag")
	}
	if err := ValidateDigest("sha256:" + string(make([]byte, 64))); err == nil {
		t.Fatal("accepted invalid digest")
	}
	if err := ValidateDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
}
