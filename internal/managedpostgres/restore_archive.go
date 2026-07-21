package managedpostgres

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

const (
	postgresRestoreListPath = "/tmp/platformd-postgres-restore.list"
	maximumRestoreListBytes = 64 << 20
)

// prepareRestoreArchive asks pg_restore for the archive TOC before importing.
// The bytes read (including exec-layer read-ahead) are replayed from a secure
// temporary file, so the original backup remains a single-pass stream.
func (controller *Controller) prepareRestoreArchive(
	ctx context.Context,
	containerID string,
	dump io.Reader,
) (io.Reader, []byte, []string, func(), error) {
	prefix, err := os.CreateTemp(controller.volumeRoot, ".postgres-restore-prefix-*")
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create PostgreSQL restore prefix: %w", err)
	}
	cleanup := func() {
		_ = prefix.Close()
		_ = os.Remove(prefix.Name())
	}
	source := &postgresContextReader{ctx: ctx, source: dump}
	var list limitedRestoreList
	var stderr boundedDiagnostic
	code, execErr := controller.engine.ExecContainer(ctx, containerID, containerengine.ExecRequest{
		Command: []string{"pg_restore", "--list"},
		Stdin:   io.TeeReader(source, prefix), Stdout: &list, Stderr: &stderr,
	})
	if execErr != nil || code != 0 {
		cleanup()
		if execErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("list PostgreSQL backup archive with code %d: %s: %w", code, stderr.String(), execErr)
		}
		return nil, nil, nil, nil, fmt.Errorf("list PostgreSQL backup archive with code %d: %s", code, stderr.String())
	}
	if _, err := prefix.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, nil, nil, fmt.Errorf("rewind PostgreSQL restore prefix: %w", err)
	}
	restoreList, extensions, err := filterRestoreList(list.Bytes())
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return &postgresContextReader{ctx: ctx, source: io.MultiReader(prefix, source)}, restoreList, extensions, cleanup, nil
}

// Extension creation and metadata require the bootstrap superuser. The
// extension script already installs its canonical comment, so those two TOC
// entries are omitted while all application-owned comments remain restorable.
func filterRestoreList(input []byte) ([]byte, []string, error) {
	result := bytes.NewBuffer(make([]byte, 0, len(input)+1))
	extensions := make([]string, 0)
	seenExtensions := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		exclude := false
		if len(fields) >= 6 && fields[3] == "EXTENSION" && fields[4] == "-" {
			name := strings.Join(fields[5:], " ")
			if _, exists := seenExtensions[name]; !exists {
				seenExtensions[name] = struct{}{}
				extensions = append(extensions, name)
			}
			exclude = true
		} else if len(fields) >= 7 && fields[3] == "COMMENT" && fields[4] == "-" && fields[5] == "EXTENSION" {
			exclude = true
		}
		if exclude && !strings.HasPrefix(line, ";") {
			result.WriteByte(';')
		}
		result.WriteString(line)
		result.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("parse PostgreSQL restore list: %w", err)
	}
	return result.Bytes(), extensions, nil
}

func (controller *Controller) prepareRestoreExtensions(
	ctx context.Context,
	resource state.ManagedPostgres,
	candidate containerengine.Container,
	networkName string,
	names []string,
) error {
	if len(names) == 0 {
		return nil
	}
	connection, err := controller.bootstrapConnection(ctx, activeRuntime{
		resource: resource, container: candidate, network: networkName,
	})
	if err != nil {
		return fmt.Errorf("open restore candidate extensions: %w", err)
	}
	var changeErr error
	for _, name := range names {
		if err := connection.ChangeExtension(ctx, name, true); err != nil {
			changeErr = fmt.Errorf("create extension %q: %w", name, err)
			break
		}
	}
	if err := errors.Join(changeErr, connection.Close(ctx)); err != nil {
		return fmt.Errorf("prepare PostgreSQL restore extensions: %w", err)
	}
	return nil
}

func (controller *Controller) writeRestoreList(ctx context.Context, containerID string, restoreList []byte) error {
	var stderr boundedDiagnostic
	code, execErr := controller.engine.ExecContainer(ctx, containerID, containerengine.ExecRequest{
		Command: []string{"sh", "-c", "umask 077; cat > " + postgresRestoreListPath},
		Stdin:   bytes.NewReader(restoreList), Stderr: &stderr,
	})
	if execErr != nil || code != 0 {
		if execErr != nil {
			return fmt.Errorf("write PostgreSQL restore list with code %d: %s: %w", code, stderr.String(), execErr)
		}
		return fmt.Errorf("write PostgreSQL restore list with code %d: %s", code, stderr.String())
	}
	return nil
}

func (controller *Controller) removeRestoreList(ctx context.Context, containerID string) error {
	var stderr boundedDiagnostic
	code, execErr := controller.engine.ExecContainer(ctx, containerID, containerengine.ExecRequest{
		Command: []string{"rm", "-f", postgresRestoreListPath}, Stderr: &stderr,
	})
	if execErr != nil {
		return fmt.Errorf("remove PostgreSQL restore list with code %d: %s: %w", code, stderr.String(), execErr)
	}
	if code != 0 {
		return fmt.Errorf("remove PostgreSQL restore list with code %d: %s", code, stderr.String())
	}
	return nil
}

type limitedRestoreList struct{ buffer bytes.Buffer }

func (list *limitedRestoreList) Write(value []byte) (int, error) {
	if len(value) > maximumRestoreListBytes-list.buffer.Len() {
		return 0, errors.New("PostgreSQL restore list exceeds the 64 MiB safety limit")
	}
	return list.buffer.Write(value)
}

func (list *limitedRestoreList) Bytes() []byte { return list.buffer.Bytes() }
