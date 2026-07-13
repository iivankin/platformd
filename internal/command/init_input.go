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
	"github.com/iivankin/platformd/internal/disasterrestore"
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

func restoreInputProvider(inputFD int) (func() (disasterrestore.ValidatedInput, error), error) {
	if inputFD >= 0 {
		if term.IsTerminal(inputFD) {
			return nil, errors.New("--input-fd must not reference a terminal")
		}
		return func() (disasterrestore.ValidatedInput, error) {
			file := os.NewFile(uintptr(inputFD), "platformd-restore-input")
			if file == nil {
				return disasterrestore.ValidatedInput{}, errors.New("--input-fd is invalid")
			}
			defer file.Close()
			input, err := disasterrestore.ReadInput(file)
			if err != nil {
				return disasterrestore.ValidatedInput{}, err
			}
			return disasterrestore.ValidateInput(input)
		}, nil
	}
	return readInteractiveRestoreInput, nil
}

func consolePassphraseProvider(inputFD int) (func() ([]byte, error), error) {
	if inputFD >= 0 {
		if term.IsTerminal(inputFD) {
			return nil, errors.New("--input-fd must not reference a terminal")
		}
		return func() ([]byte, error) {
			file := os.NewFile(uintptr(inputFD), "platformd-console-passphrase-input")
			if file == nil {
				return nil, errors.New("--input-fd is invalid")
			}
			defer file.Close()
			return bootstrap.ReadConsolePassphraseInput(file)
		}, nil
	}
	return readInteractiveConsolePassphrase, nil
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
	passphrase, err := promptConfirmedSecret(tty, "Console passphrase: ", "Confirm console passphrase: ")
	if err != nil {
		return bootstrap.ValidatedInput{}, err
	}
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

func readInteractiveConsolePassphrase() ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("console passphrase reset requires a root TTY or --input-fd")
	}
	defer tty.Close()
	return promptConfirmedSecret(tty, "New console passphrase: ", "Confirm new console passphrase: ")
}

func promptConfirmedSecret(tty *os.File, label, confirmationLabel string) ([]byte, error) {
	value, err := promptSecret(tty, label)
	if err != nil {
		return nil, err
	}
	confirmation, err := promptSecret(tty, confirmationLabel)
	if err != nil {
		clear(value)
		return nil, err
	}
	defer clear(confirmation)
	if string(value) != string(confirmation) {
		clear(value)
		return nil, errors.New("console passphrases do not match")
	}
	return value, nil
}

func readInteractiveRestoreInput() (disasterrestore.ValidatedInput, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return disasterrestore.ValidatedInput{}, errors.New("interactive restore requires a root TTY or --input-fd")
	}
	defer tty.Close()
	master, err := promptSecret(tty, "Master recovery key: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	defer clear(master)
	endpoint, err := prompt(tty, "Remote S3 endpoint: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	region, err := prompt(tty, "Remote S3 region: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	bucket, err := prompt(tty, "Remote S3 bucket: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	prefix, err := prompt(tty, "Remote S3 prefix (empty is allowed): ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	accessKey, err := prompt(tty, "Remote S3 access key ID: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	secret, err := promptSecret(tty, "Remote S3 secret access key: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	defer clear(secret)
	override, err := prompt(tty, "Override restored Cloudflare Access team/AUD? [y/N]: ")
	if err != nil {
		return disasterrestore.ValidatedInput{}, err
	}
	var team, audience string
	if answer := strings.ToLower(strings.TrimSpace(override)); answer == "y" || answer == "yes" {
		team, err = prompt(tty, "Cloudflare Access team domain override: ")
		if err != nil {
			return disasterrestore.ValidatedInput{}, err
		}
		audience, err = prompt(tty, "Cloudflare Access application AUD override: ")
		if err != nil {
			return disasterrestore.ValidatedInput{}, err
		}
	} else if answer != "" && answer != "n" && answer != "no" {
		return disasterrestore.ValidatedInput{}, errors.New("Access override answer must be yes or no")
	}
	return disasterrestore.ValidateInput(disasterrestore.Input{
		MasterRecoveryKey: string(master), Endpoint: endpoint, Region: region, Bucket: bucket, Prefix: prefix,
		AccessKeyID: accessKey, SecretAccessKey: string(secret),
		AccessTeamDomainOverride: team, AccessAudienceOverride: audience,
	})
}

func promptSecret(tty *os.File, label string) ([]byte, error) {
	if _, err := io.WriteString(tty, label); err != nil {
		return nil, err
	}
	value, err := term.ReadPassword(int(tty.Fd()))
	_, _ = io.WriteString(tty, "\n")
	if err != nil {
		clear(value)
		return nil, err
	}
	if len(value) == 0 || len(value) > maximumPromptLineBytes {
		clear(value)
		return nil, errors.New("secret input size is outside bounds")
	}
	return value, nil
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
