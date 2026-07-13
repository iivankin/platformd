package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// RejectDuplicateKeys validates one complete JSON value while preserving the
// caller's concrete decoding rules. encoding/json otherwise silently accepts
// duplicate object keys, which is unsuitable for signed or encrypted metadata.
func RejectDuplicateKeys(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := walkValue(decoder); err != nil {
		return errors.New("JSON contains duplicate keys or is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func walkValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	wanted := json.Delim('}')
	if delimiter == '[' {
		wanted = ']'
	}
	if closing != wanted {
		return errors.New("unexpected JSON closing delimiter")
	}
	return nil
}
