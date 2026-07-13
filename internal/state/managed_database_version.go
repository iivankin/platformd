package state

import (
	"errors"

	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/opencontainers/go-digest"
)

type managedDatabaseImageSwitch struct {
	ExpectedTag    string
	ExpectedDigest string
	Tag            string
	Digest         string
}

func validateManagedDatabaseImageSwitch(engine managedimages.Engine, image managedDatabaseImageSwitch) error {
	if image.ExpectedTag == "" || image.ExpectedDigest == "" || image.Tag == "" || image.Digest == "" ||
		image.ExpectedDigest == image.Digest {
		return errors.New("managed database version change image input is invalid")
	}
	if _, err := managedimages.Reference(engine, image.ExpectedTag); err != nil {
		return err
	}
	if _, err := managedimages.Reference(engine, image.Tag); err != nil {
		return err
	}
	for _, value := range []string{image.ExpectedDigest, image.Digest} {
		parsed, err := digest.Parse(value)
		if err != nil || parsed.Validate() != nil {
			return errors.New("managed database version change digest is invalid")
		}
	}
	return nil
}
