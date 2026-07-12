package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/iivankin/platformd/internal/bootstrap"
)

const maximumPromptLineBytes = 4096

func bootstrapInputProvider(inputFD int) (func() (bootstrap.ValidatedInput, error), error) {
	if inputFD >= 0 {
		if term.IsTerminal(inputFD) {
			return nil, errors.New("--input-fd must not reference a terminal")
		}
		return func() (bootstrap.ValidatedInput, error) {
			file := os.NewFile(uintptr(inputFD), "platformd-init-input")
			if file == nil {
				return bootstrap.ValidatedInput{}, errors.New("--input-fd is invalid")
			}
			defer file.Close()
			input, err := bootstrap.ReadInput(file)
			if err != nil {
				return bootstrap.ValidatedInput{}, err
			}
			return bootstrap.ValidateInput(input)
		}, nil
	}
	return readInteractiveInput, nil
}

func confirmRecoveryKey(recoveryKey string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return errors.New("master-key confirmation requires an interactive root TTY")
	}
	defer tty.Close()
	if _, err := fmt.Fprintf(tty, "\nSave this platformd master key outside the VPS:\n\n%s\n\nКлюч сохранён вне VPS? [y/N] ", recoveryKey); err != nil {
		return err
	}
	answer, err := readTTYLine(tty, 16)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "yes" && strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return errors.New("master key was not confirmed as saved")
	}
	return nil
}

func readInteractiveInput() (bootstrap.ValidatedInput, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return bootstrap.ValidatedInput{}, errors.New("interactive init requires a root TTY or --input-fd")
	}
	defer tty.Close()
	adminHostname, err := prompt(tty, "Admin hostname: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	automationHostname, err := prompt(tty, "Automation API/MCP hostname (empty to disable): ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	teamDomain, err := prompt(tty, "Cloudflare Access team domain: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	audience, err := prompt(tty, "Cloudflare Access application AUD: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	certificatePath, err := prompt(tty, "Origin certificate PEM path: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	privateKeyPath, err := prompt(tty, "Origin private key PEM path: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	certificate, err := readRootFile(certificatePath, false)
	if err != nil {
		return bootstrap.ValidatedInput{}, fmt.Errorf("read Origin certificate: %w", err)
	}
	privateKey, err := readRootFile(privateKeyPath, true)
	if err != nil {
		return bootstrap.ValidatedInput{}, fmt.Errorf("read Origin private key: %w", err)
	}
	defer clear(privateKey)
	if _, err := io.WriteString(tty, "Console passphrase: "); err != nil {
		return bootstrap.ValidatedInput{}, err
	}
	passphrase, err := term.ReadPassword(int(tty.Fd()))
	if _, writeErr := io.WriteString(tty, "\nConfirm console passphrase: "); err == nil && writeErr != nil {
		err = writeErr
	}
	if err != nil {
		clear(passphrase)
		return bootstrap.ValidatedInput{}, err
	}
	confirmation, err := term.ReadPassword(int(tty.Fd()))
	_, _ = io.WriteString(tty, "\n")
	if err != nil {
		clear(passphrase)
		clear(confirmation)
		return bootstrap.ValidatedInput{}, err
	}
	if string(passphrase) != string(confirmation) {
		clear(passphrase)
		clear(confirmation)
		return bootstrap.ValidatedInput{}, errors.New("console passphrases do not match")
	}
	clear(confirmation)
	input := bootstrap.Input{
		AdminHostname:        adminHostname,
		AutomationHostname:   automationHostname,
		AccessTeamDomain:     teamDomain,
		AccessAudience:       audience,
		ConsolePassphrase:    string(passphrase),
		OriginCertificatePEM: string(certificate),
		OriginPrivateKeyPEM:  string(privateKey),
	}
	clear(passphrase)
	return bootstrap.ValidateInput(input)
}

func prompt(tty *os.File, label string) (string, error) {
	if _, err := io.WriteString(tty, label); err != nil {
		return "", err
	}
	return readTTYLine(tty, maximumPromptLineBytes)
}

func readTTYLine(tty *os.File, maximum int) (string, error) {
	value := make([]byte, 0, min(maximum, 256))
	var one [1]byte
	for len(value) <= maximum {
		count, err := tty.Read(one[:])
		if count == 1 {
			if one[0] == '\n' {
				return strings.TrimSuffix(string(value), "\r"), nil
			}
			value = append(value, one[0])
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("interactive input line exceeds limit")
}

func readRootFile(path string, private bool) ([]byte, error) {
	path = strings.TrimSpace(path)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular non-symlink file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return nil, errors.New("file must be owned by root")
	}
	if private && info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("private key mode = %04o, want 0600", info.Mode().Perm())
	}
	if !private && info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("certificate file must not be group/world writable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(value) > 1<<20 {
		clear(value)
		return nil, errors.New("PEM file exceeds 1 MiB")
	}
	return value, nil
}
