package passphrase

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memoryKiB   = 19 * 1024
	iterations  = 2
	parallelism = 1
	saltLength  = 16
	hashLength  = 32
	maxLength   = 1024
)

var encoding = base64.RawStdEncoding

func Hash(value []byte) (string, error) {
	return HashWith(value, rand.Reader)
}

func HashWith(value []byte, random io.Reader) (string, error) {
	if err := validateValue(value); err != nil {
		return "", err
	}
	salt := make([]byte, saltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate Argon2id salt: %w", err)
	}
	hash := argon2.IDKey(value, salt, iterations, memoryKiB, parallelism, hashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memoryKiB, iterations, parallelism, encoding.EncodeToString(salt), encoding.EncodeToString(hash)), nil
}

func Verify(encoded string, value []byte) (bool, error) {
	if err := validateValue(value); err != nil {
		return false, err
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("invalid Argon2id verifier format")
	}
	version, err := parseSingleParameter(parts[2], "v")
	if err != nil || version != argon2.Version {
		return false, errors.New("unsupported Argon2id version")
	}
	parameters, err := parseParameters(parts[3])
	if err != nil {
		return false, err
	}
	if parameters.memory != memoryKiB || parameters.iterations != iterations || parameters.parallelism != parallelism {
		return false, errors.New("unsupported Argon2id parameters")
	}
	salt, err := encoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltLength {
		return false, errors.New("invalid Argon2id salt")
	}
	expected, err := encoding.DecodeString(parts[5])
	if err != nil || len(expected) != hashLength {
		return false, errors.New("invalid Argon2id hash")
	}
	actual := argon2.IDKey(value, salt, parameters.iterations, parameters.memory, parameters.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func validateValue(value []byte) error {
	if len(value) == 0 {
		return errors.New("passphrase is empty")
	}
	if len(value) > maxLength {
		return fmt.Errorf("passphrase is longer than %d bytes", maxLength)
	}
	return nil
}

type argonParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseParameters(value string) (argonParameters, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return argonParameters{}, errors.New("invalid Argon2id parameter list")
	}
	memory, err := parseSingleParameter(parts[0], "m")
	if err != nil {
		return argonParameters{}, err
	}
	timeCost, err := parseSingleParameter(parts[1], "t")
	if err != nil {
		return argonParameters{}, err
	}
	parallel, err := parseSingleParameter(parts[2], "p")
	if err != nil || parallel > 255 {
		return argonParameters{}, errors.New("invalid Argon2id parallelism")
	}
	return argonParameters{memory: uint32(memory), iterations: uint32(timeCost), parallelism: uint8(parallel)}, nil
}

func parseSingleParameter(value, name string) (uint64, error) {
	prefix := name + "="
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("missing Argon2id %s parameter", name)
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid Argon2id %s parameter", name)
	}
	return parsed, nil
}
