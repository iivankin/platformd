package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/releasemanifest"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	var supportedFrom stringList
	binaryPath := flag.String("binary", "", "bundled platformd binary")
	binaryURL := flag.String("binary-url", "", "published HTTPS binary URL")
	version := flag.String("version", "", "strict target SemVer")
	privateKeyPath := flag.String("private-key", "", "Ed25519 PKCS#8 PEM private key")
	outputPath := flag.String("output", "", "output manifest path")
	flag.Var(&supportedFrom, "supported-from", "exact supported source version (repeatable)")
	flag.Parse()
	if *binaryPath == "" || *binaryURL == "" || *version == "" || *privateKeyPath == "" || *outputPath == "" || flag.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: go run ./tools/release-manifest --binary <path> --binary-url <https-url> --version <semver> --private-key <pem> --output <path> [--supported-from <semver> ...]")
		os.Exit(2)
	}
	if err := run(*binaryPath, *binaryURL, *version, *privateKeyPath, *outputPath, supportedFrom); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run(binaryPath, binaryURL, version, privateKeyPath, outputPath string, supportedFrom []string) error {
	info, err := os.Lstat(binaryPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("binary must be a non-empty regular file")
	}
	binary, err := os.Open(binaryPath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, binary)
	closeErr := binary.Close()
	if hashErr != nil || closeErr != nil {
		return errors.Join(hashErr, closeErr)
	}
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	manifest, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture:  "amd64",
		BinarySHA256:  hex.EncodeToString(hash.Sum(nil)),
		BinarySize:    info.Size(),
		BinaryURL:     binaryURL,
		Format:        releasemanifest.FormatVersion,
		OS:            "linux",
		SupportedFrom: supportedFrom,
		Version:       version,
	}, privateKey)
	clear(privateKey)
	if err != nil {
		return err
	}
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return err
	}
	if _, err := releasemanifest.ParseAndVerify(manifest, publicKey); err != nil {
		return fmt.Errorf("verify manifest with embedded release public key: %w", err)
	}
	return writeAtomic(outputPath, manifest)
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, rest := pem.Decode(value)
	validRest := len(strings.TrimSpace(string(rest))) == 0
	clear(value)
	if block == nil || block.Type != "PRIVATE KEY" || !validRest {
		return nil, errors.New("private key must contain one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	clear(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not Ed25519")
	}
	return privateKey, nil
}

func writeAtomic(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".release-manifest-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
