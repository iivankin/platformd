package releasemanifest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/semver"
)

const (
	FormatVersion       = 1
	maximumManifestSize = 64 << 10
	maximumBinarySize   = 1 << 30
	maximumSupported    = 256
)

var base64URL = base64.RawURLEncoding

type Manifest struct {
	Architecture  string   `json:"architecture"`
	BinarySHA256  string   `json:"binarySha256"`
	BinarySize    int64    `json:"binarySize"`
	BinaryURL     string   `json:"binaryUrl"`
	Format        int      `json:"formatVersion"`
	OS            string   `json:"os"`
	SupportedFrom []string `json:"supportedFrom"`
	Version       string   `json:"version"`
	Signature     string   `json:"signature"`
}

func ParseAndVerify(value []byte, publicKey ed25519.PublicKey) (Manifest, error) {
	if len(value) == 0 || len(value) > maximumManifestSize {
		return Manifest{}, errors.New("release manifest size is outside bounds")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("release public key is invalid")
	}
	if err := rejectDuplicateTopLevelKeys(value); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("release manifest contains trailing JSON")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(value, manifest.canonicalSigned()) {
		return Manifest{}, errors.New("release manifest JSON is not canonical")
	}
	signature, err := base64URL.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Manifest{}, errors.New("release manifest signature is invalid")
	}
	if !ed25519.Verify(publicKey, manifest.canonicalUnsigned(), signature) {
		return Manifest{}, errors.New("release manifest signature verification failed")
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.Format != FormatVersion || manifest.OS != "linux" || manifest.Architecture != "amd64" {
		return errors.New("release manifest target or format is unsupported")
	}
	if manifest.BinarySize <= 0 || manifest.BinarySize > maximumBinarySize {
		return errors.New("release binary size is outside bounds")
	}
	if !validSHA256(manifest.BinarySHA256) {
		return errors.New("release binary SHA-256 is invalid")
	}
	parsedURL, err := url.Parse(manifest.BinaryURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" || len(manifest.BinaryURL) > 2048 || !printableASCII(manifest.BinaryURL) {
		return errors.New("release binary URL must be bounded HTTPS without userinfo or fragment")
	}
	version, err := semver.Parse(manifest.Version)
	if err != nil {
		return fmt.Errorf("release version: %w", err)
	}
	_ = version
	if len(manifest.SupportedFrom) > maximumSupported {
		return errors.New("release supportedFrom exceeds limit")
	}
	var previous semver.Version
	for index, value := range manifest.SupportedFrom {
		parsed, err := semver.Parse(value)
		if err != nil {
			return fmt.Errorf("release supportedFrom: %w", err)
		}
		if index > 0 && semver.Compare(previous, parsed) >= 0 {
			return errors.New("release supportedFrom must be strictly SemVer-sorted")
		}
		previous = parsed
	}
	if manifest.Signature == "" || len(manifest.Signature) > 128 {
		return errors.New("release manifest signature is missing or too large")
	}
	return nil
}

func (manifest Manifest) AllowsUpdateFrom(installed string) error {
	installedVersion, err := semver.Parse(installed)
	if err != nil {
		return fmt.Errorf("installed version: %w", err)
	}
	targetVersion, err := semver.Parse(manifest.Version)
	if err != nil {
		return fmt.Errorf("target version: %w", err)
	}
	if semver.Compare(targetVersion, installedVersion) <= 0 {
		return errors.New("release version is not strictly newer than installed version")
	}
	for _, allowed := range manifest.SupportedFrom {
		if allowed == installed {
			return nil
		}
	}
	return errors.New("installed version is absent from release supportedFrom")
}

func (manifest Manifest) VerifyBinary(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect release binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.BinarySize {
		return errors.New("release binary type or size mismatch")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release binary: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximumBinarySize+1))
	if err != nil {
		return fmt.Errorf("hash release binary: %w", err)
	}
	if written != manifest.BinarySize || hex.EncodeToString(hash.Sum(nil)) != manifest.BinarySHA256 {
		return errors.New("release binary SHA-256 mismatch")
	}
	return nil
}

func Sign(manifest Manifest, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("release private key is invalid")
	}
	manifest.Signature = base64URL.EncodeToString(make([]byte, ed25519.SignatureSize))
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	manifest.Signature = base64URL.EncodeToString(ed25519.Sign(privateKey, manifest.canonicalUnsigned()))
	return manifest.canonicalSigned(), nil
}

func (manifest Manifest) canonicalUnsigned() []byte {
	var buffer bytes.Buffer
	buffer.WriteString(`{"architecture":`)
	writeString(&buffer, manifest.Architecture)
	buffer.WriteString(`,"binarySha256":`)
	writeString(&buffer, manifest.BinarySHA256)
	buffer.WriteString(`,"binarySize":`)
	buffer.WriteString(strconv.FormatInt(manifest.BinarySize, 10))
	buffer.WriteString(`,"binaryUrl":`)
	writeString(&buffer, manifest.BinaryURL)
	buffer.WriteString(`,"formatVersion":`)
	buffer.WriteString(strconv.Itoa(manifest.Format))
	buffer.WriteString(`,"os":`)
	writeString(&buffer, manifest.OS)
	buffer.WriteString(`,"supportedFrom":[`)
	for index, value := range manifest.SupportedFrom {
		if index > 0 {
			buffer.WriteByte(',')
		}
		writeString(&buffer, value)
	}
	buffer.WriteString(`],"version":`)
	writeString(&buffer, manifest.Version)
	buffer.WriteByte('}')
	return buffer.Bytes()
}

func (manifest Manifest) canonicalSigned() []byte {
	var buffer bytes.Buffer
	buffer.WriteString(`{"architecture":`)
	writeString(&buffer, manifest.Architecture)
	buffer.WriteString(`,"binarySha256":`)
	writeString(&buffer, manifest.BinarySHA256)
	buffer.WriteString(`,"binarySize":`)
	buffer.WriteString(strconv.FormatInt(manifest.BinarySize, 10))
	buffer.WriteString(`,"binaryUrl":`)
	writeString(&buffer, manifest.BinaryURL)
	buffer.WriteString(`,"formatVersion":`)
	buffer.WriteString(strconv.Itoa(manifest.Format))
	buffer.WriteString(`,"os":`)
	writeString(&buffer, manifest.OS)
	buffer.WriteString(`,"signature":`)
	writeString(&buffer, manifest.Signature)
	buffer.WriteString(`,"supportedFrom":[`)
	for index, value := range manifest.SupportedFrom {
		if index > 0 {
			buffer.WriteByte(',')
		}
		writeString(&buffer, value)
	}
	buffer.WriteString(`],"version":`)
	writeString(&buffer, manifest.Version)
	buffer.WriteByte('}')
	return buffer.Bytes()
}

func writeString(buffer *bytes.Buffer, value string) {
	buffer.WriteString(strconv.Quote(value))
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func printableASCII(value string) bool {
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func rejectDuplicateTopLevelKeys(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("release manifest must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("release manifest object is malformed")
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("release manifest key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("release manifest contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return errors.New("release manifest value is malformed")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("release manifest object is malformed")
	}
	return nil
}
