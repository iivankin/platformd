package objectstore

import (
	"crypto/hkdf"
	"crypto/sha256"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const storeKeyDomain = "platformd/s3/store/v1"

func deriveStoreKey(master cryptobox.MasterKey, storeID string) ([32]byte, error) {
	value, err := hkdf.Key(sha256.New, master[:], []byte(storeID), storeKeyDomain, 32)
	if err != nil {
		return [32]byte{}, err
	}
	var key [32]byte
	copy(key[:], value)
	clear(value)
	return key, nil
}
