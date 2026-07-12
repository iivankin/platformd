package releaseconfig

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	PublicKeyBase64URL = "WCUFfw1s7MCGOyxsbUvA66E0Hs-KhkI-uQchI8z_ZTE"
	LatestManifestURL  = "https://github.com/iivankin/platformd/releases/latest/download/platformd-linux-amd64.manifest.json"
	versionManifestURL = "https://github.com/iivankin/platformd/releases/download/v%s/platformd-linux-amd64.manifest.json"
)

func PublicKey() (ed25519.PublicKey, error) {
	value, err := base64.RawURLEncoding.DecodeString(PublicKeyBase64URL)
	if err != nil || len(value) != ed25519.PublicKeySize {
		return nil, errors.New("embedded release public key is invalid")
	}
	return ed25519.PublicKey(value), nil
}

func VersionManifestURL(version string) string {
	return fmt.Sprintf(versionManifestURL, version)
}
