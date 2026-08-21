package hostagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	HostID    string `json:"hostId"`
	HostToken string `json:"hostToken"`
	ParentURL string `json:"parentUrl"`
	Name      string `json:"name"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return Config{}, err
	}
	if config.HostID == "" || config.HostToken == "" || config.ParentURL == "" {
		return Config{}, errors.New("worker config is incomplete")
	}
	parentURL, err := NormalizeParentURL(config.ParentURL)
	if err != nil {
		return Config{}, err
	}
	config.ParentURL = parentURL
	return config, nil
}

func Save(path string, config Config) error {
	if config.HostID == "" || config.HostToken == "" || config.ParentURL == "" {
		return errors.New("worker config is incomplete")
	}
	parentURL, err := NormalizeParentURL(config.ParentURL)
	if err != nil {
		return err
	}
	config.ParentURL = parentURL
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".worker-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(append(raw, '\n'))
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(writeErr, syncErr, closeErr)
	}
	return os.Rename(temporaryPath, path)
}
