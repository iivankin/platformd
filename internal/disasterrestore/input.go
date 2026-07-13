package disasterrestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/remotes3"
)

const maximumInputBytes = 1 << 20

type Input struct {
	MasterRecoveryKey        string `json:"masterRecoveryKey"`
	Endpoint                 string `json:"endpoint"`
	Region                   string `json:"region"`
	Bucket                   string `json:"bucket"`
	Prefix                   string `json:"prefix"`
	AccessKeyID              string `json:"accessKeyId"`
	SecretAccessKey          string `json:"secretAccessKey"`
	AccessTeamDomainOverride string `json:"accessTeamDomainOverride"`
	AccessAudienceOverride   string `json:"accessAudienceOverride"`
}

type ValidatedInput struct {
	Master           cryptobox.MasterKey
	Remote           remotes3.Config
	AccessTeamDomain *string
	AccessAudience   *string
}

func ReadInput(reader io.Reader) (Input, error) {
	value, err := io.ReadAll(io.LimitReader(reader, maximumInputBytes+1))
	if err != nil {
		return Input{}, err
	}
	if len(value) == 0 || len(value) > maximumInputBytes {
		return Input{}, errors.New("restore input size is outside bounds")
	}
	if err := rejectDuplicateTopLevelKeys(value); err != nil {
		return Input{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var input Input
	if err := decoder.Decode(&input); err != nil {
		return Input{}, fmt.Errorf("decode restore input: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Input{}, errors.New("restore input contains trailing JSON")
	}
	return input, nil
}

func ValidateInput(input Input) (ValidatedInput, error) {
	master, err := masterkey.ParseRecoveryString(strings.TrimSpace(input.MasterRecoveryKey))
	if err != nil {
		return ValidatedInput{}, err
	}
	remote, err := remotes3.CanonicalConfig(remotes3.Config{
		Endpoint: input.Endpoint, Region: input.Region, Bucket: input.Bucket, Prefix: input.Prefix,
		AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey,
	})
	if err != nil {
		return ValidatedInput{}, fmt.Errorf("restore target: %w", err)
	}
	teamValue := strings.TrimSpace(input.AccessTeamDomainOverride)
	audienceValue := strings.TrimSpace(input.AccessAudienceOverride)
	if (teamValue == "") != (audienceValue == "") {
		return ValidatedInput{}, errors.New("Access team domain and audience overrides must be supplied together")
	}
	var team, audience *string
	if teamValue != "" {
		canonicalTeam, canonicalAudience, err := bootstrap.ValidateAccessConfiguration(teamValue, audienceValue)
		if err != nil {
			return ValidatedInput{}, err
		}
		team = &canonicalTeam
		audience = &canonicalAudience
	}
	return ValidatedInput{Master: master, Remote: remote, AccessTeamDomain: team, AccessAudience: audience}, nil
}

func rejectDuplicateTopLevelKeys(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("restore input must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return errors.New("restore input JSON is malformed")
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("restore input key is invalid")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("restore input contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return errors.New("restore input JSON is malformed")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("restore input JSON is malformed")
	}
	return nil
}
