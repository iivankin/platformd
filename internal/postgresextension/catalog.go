package postgresextension

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/iivankin/platformd/internal/state"
	"github.com/opencontainers/go-digest"
)

const (
	VectorName      = "vector"
	VectorVersion   = "0.8.5"
	vectorSourceURL = "https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.5.tar.gz"
	vectorSourceSHA = "6f88a5cbdde31666f4b6c1a6b75c51dcbeffe58f9a7d2b26e502d5a6e5e14d44"
	recipeFormat    = "platformd-postgres-extension-v1"
	DerivedOwner    = "postgres-extension"
	OwnerLabel      = "io.platformd.owner"
	CacheKeyLabel   = "io.platformd.postgres-extension.cache-key"
	BaseDigestLabel = "io.platformd.postgres-extension.base-digest"
	RecipeSetLabel  = "io.platformd.postgres-extension.recipe-set"
)

var (
	ErrUnsupportedExtension = errors.New("PostgreSQL extension is not provisionable")
	ErrRecipeMismatch       = errors.New("PostgreSQL extension recipe is not supported by this platformd release")
)

type Recipe struct {
	Name         string
	Version      string
	SourceURL    string
	SourceSHA256 string
	Digest       string
}

func VectorRecipe() Recipe {
	value := strings.Join([]string{
		recipeFormat,
		VectorName,
		VectorVersion,
		vectorSourceURL,
		vectorSourceSHA,
		"debian-build-script-v1",
	}, "\n")
	return Recipe{
		Name: VectorName, Version: VectorVersion, SourceURL: vectorSourceURL,
		SourceSHA256: vectorSourceSHA, Digest: digest.FromString(value).String(),
	}
}

func Lookup(name string) (Recipe, error) {
	if strings.TrimSpace(name) != VectorName {
		return Recipe{}, ErrUnsupportedExtension
	}
	return VectorRecipe(), nil
}

func ValidateDesired(extension state.ManagedPostgresExtension) (Recipe, error) {
	recipe, err := Lookup(extension.Name)
	if err != nil {
		return Recipe{}, err
	}
	if extension.Version != recipe.Version || extension.RecipeDigest != recipe.Digest {
		return Recipe{}, ErrRecipeMismatch
	}
	return recipe, nil
}

func CacheKey(baseDigest, architecture string, extensions []state.ManagedPostgresExtension) (string, error) {
	if baseDigest == "" || architecture == "" || len(extensions) == 0 {
		return "", errors.New("PostgreSQL extension cache key input is incomplete")
	}
	values := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		recipe, err := ValidateDesired(extension)
		if err != nil {
			return "", err
		}
		values = append(values, recipe.Name+"@"+recipe.Version+"@"+recipe.Digest)
	}
	sort.Strings(values)
	payload := strings.Join(append([]string{recipeFormat, baseDigest, architecture}, values...), "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:]), nil
}

func RecipeSet(extensions []state.ManagedPostgresExtension) (string, error) {
	values := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		recipe, err := ValidateDesired(extension)
		if err != nil {
			return "", err
		}
		values = append(values, recipe.Name+"@"+recipe.Version+"@"+recipe.Digest)
	}
	sort.Strings(values)
	return strings.Join(values, ","), nil
}
