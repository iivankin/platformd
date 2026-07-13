package disasterrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const maximumImporterOutput = 2 << 20

type ExactImporter func(context.Context, string, ImportPayload) (ImportResult, error)

func RunExactImporter(ctx context.Context, executable string, payload ImportPayload) (ImportResult, error) {
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return ImportResult{}, errors.New("saved restore importer is not a regular file")
	}
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return ImportResult{}, err
	}
	command := exec.CommandContext(ctx, executable, "__restore-import")
	command.ExtraFiles = []*os.File{readPipe}
	stdout := boundedBuffer{maximum: maximumImporterOutput}
	stderr := boundedBuffer{maximum: maximumImporterOutput}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		_ = readPipe.Close()
		_ = writePipe.Close()
		return ImportResult{}, err
	}
	_ = readPipe.Close()
	writeResult := make(chan error, 1)
	go func() {
		encoder := json.NewEncoder(writePipe)
		writeResult <- errors.Join(encoder.Encode(payload), writePipe.Close())
	}()
	waitErr := command.Wait()
	writeErr := <-writeResult
	if waitErr != nil || writeErr != nil {
		return ImportResult{}, fmt.Errorf("saved restore importer failed: %w: %s", errors.Join(waitErr, writeErr), stderr.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var result ImportResult
	if err := decoder.Decode(&result); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return ImportResult{}, errors.New("saved restore importer returned invalid JSON")
	}
	if result.AdminHostname == "" || result.OriginCertificatePEM == "" {
		return ImportResult{}, errors.New("saved restore importer returned incomplete result")
	}
	return result, nil
}

type boundedBuffer struct {
	bytes.Buffer
	maximum int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.maximum-buffer.Len() {
		return 0, errors.New("restore importer output exceeds limit")
	}
	return buffer.Buffer.Write(value)
}
