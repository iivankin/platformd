package registryname

import (
	"errors"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

func ValidateRepository(value string) error {
	if value == "" || len(value) > reference.RepositoryNameTotalLengthMax || strings.Contains(value, "//") {
		return errors.New("repository name must contain 1..255 Distribution-compatible characters")
	}
	parsed, err := reference.Parse(value)
	if err != nil {
		return err
	}
	named, ok := parsed.(reference.Named)
	if !ok || named.Name() != value {
		return errors.New("repository name is not canonical")
	}
	if _, tagged := parsed.(reference.Tagged); tagged {
		return errors.New("repository name cannot contain a tag")
	}
	if _, digested := parsed.(reference.Digested); digested {
		return errors.New("repository name cannot contain a digest")
	}
	return nil
}

func ValidateTag(value string) error {
	parsed, err := reference.Parse("repository")
	if err != nil {
		return err
	}
	base, ok := parsed.(reference.Named)
	if !ok {
		return errors.New("internal tag validation base is invalid")
	}
	_, err = reference.WithTag(base, value)
	return err
}

func ValidateDigest(value string) error {
	parsed, err := digest.Parse(value)
	if err != nil || parsed.Algorithm() != digest.SHA256 || len(parsed.Encoded()) != 64 || value != parsed.String() {
		return errors.New("digest must be canonical lowercase sha256")
	}
	return nil
}
