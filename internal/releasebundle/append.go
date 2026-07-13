package releasebundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Append writes the v1 deterministic runtime archive after an existing ELF.
// The caller must provide an unbundled output file and a runtime directory.
func Append(executablePath, runtimeDirectory string) error {
	if existing, err := Open(executablePath); err == nil {
		_ = existing.Close()
		return errors.New("executable already contains a runtime bundle")
	} else if !errors.Is(err, ErrNoBundle) {
		return err
	}
	files, err := runtimeFiles(runtimeDirectory)
	if err != nil {
		return err
	}
	executable, err := os.OpenFile(executablePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open executable for runtime bundle: %w", err)
	}
	archive := zip.NewWriter(executable)
	manifest := Manifest{FormatVersion: formatVersion, Files: make([]ManifestFile, 0, len(files))}
	for _, source := range files {
		manifest.Files = append(manifest.Files, source.manifest)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		_ = executable.Close()
		return fmt.Errorf("encode runtime bundle manifest: %w", err)
	}
	if err := writeZIPEntry(archive, manifestName, 0o600, manifestBytes); err != nil {
		_ = executable.Close()
		return err
	}
	for _, source := range files {
		value, err := os.ReadFile(source.path)
		if err != nil {
			_ = executable.Close()
			return fmt.Errorf("read runtime source %q: %w", source.path, err)
		}
		if err := writeZIPEntry(archive, source.manifest.Path, fs.FileMode(source.manifest.Mode), value); err != nil {
			_ = executable.Close()
			return err
		}
	}
	closeArchiveErr := archive.Close()
	syncErr := executable.Sync()
	closeExecutableErr := executable.Close()
	if closeArchiveErr != nil || syncErr != nil || closeExecutableErr != nil {
		return fmt.Errorf("finalize runtime bundle: %w", errors.Join(closeArchiveErr, syncErr, closeExecutableErr))
	}
	return nil
}

type sourceFile struct {
	path     string
	manifest ManifestFile
}

func runtimeFiles(root string) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(root, func(value string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if value == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return fmt.Errorf("runtime source %q is not a regular file/directory", value)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() < 0 || info.Size() > maximumFileBytes {
			return fmt.Errorf("runtime source %q size is outside bounds", value)
		}
		relative, err := filepath.Rel(root, value)
		if err != nil {
			return err
		}
		archivePath := "runtime/" + filepath.ToSlash(relative)
		if !validRuntimePath(archivePath) {
			return fmt.Errorf("runtime source %q has invalid archive path", value)
		}
		file, err := os.Open(value)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		files = append(files, sourceFile{path: value, manifest: ManifestFile{
			Path:   archivePath,
			Mode:   uint32(info.Mode().Perm()),
			Size:   uint64(info.Size()),
			SHA256: hex.EncodeToString(hash.Sum(nil)),
		}})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate runtime bundle: %w", err)
	}
	if len(files) == 0 || len(files)+1 > maximumEntries {
		return nil, errors.New("runtime source file count is outside bounds")
	}
	sort.Slice(files, func(left, right int) bool { return files[left].manifest.Path < files[right].manifest.Path })
	manifestFiles := make([]ManifestFile, len(files))
	for index := range files {
		manifestFiles[index] = files[index].manifest
	}
	if err := validateRuntimeProfile(manifestFiles); err != nil {
		return nil, err
	}
	return files, nil
}

func writeZIPEntry(archive *zip.Writer, name string, mode fs.FileMode, value []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(mode)
	header.Extra = nil
	header.Comment = ""
	header.NonUTF8 = false
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create runtime archive entry %q: %w", name, err)
	}
	if _, err := writer.Write(value); err != nil {
		return fmt.Errorf("write runtime archive entry %q: %w", name, err)
	}
	return nil
}

func isELF(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var magic [4]byte
	_, err = io.ReadFull(file, magic[:])
	return err == nil && string(magic[:]) == "\x7fELF"
}

func ValidateExecutable(path string) error {
	if !isELF(path) {
		return fmt.Errorf("%s is not an ELF executable", strings.TrimSpace(path))
	}
	return nil
}
