package releasebundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iivankin/platformd/internal/strictjson"
)

const (
	manifestName          = "bundle-manifest.json"
	formatVersion         = 1
	maximumManifestBytes  = 1 << 20
	maximumEntries        = 64
	maximumFileBytes      = 256 << 20
	maximumAggregateBytes = 512 << 20
)

var ErrNoBundle = errors.New("executable has no platformd runtime bundle")

type Manifest struct {
	FormatVersion int            `json:"formatVersion"`
	Files         []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type Bundle struct {
	reader  *zip.ReadCloser
	files   map[string]*zip.File
	ordered []ManifestFile
}

func Open(executablePath string) (*Bundle, error) {
	info, err := os.Lstat(executablePath)
	if err != nil {
		return nil, fmt.Errorf("inspect bundled executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("bundled executable is not a regular file")
	}
	reader, err := zip.OpenReader(executablePath)
	if errors.Is(err, zip.ErrFormat) {
		return nil, ErrNoBundle
	}
	if err != nil {
		return nil, fmt.Errorf("open runtime bundle: %w", err)
	}
	cleanup := func(err error) (*Bundle, error) {
		_ = reader.Close()
		return nil, err
	}
	if reader.Comment != "" {
		return cleanup(errors.New("runtime bundle ZIP comment is forbidden"))
	}
	if err := validateArchiveEnd(executablePath); err != nil {
		return cleanup(err)
	}
	if len(reader.File) == 0 || len(reader.File) > maximumEntries {
		return cleanup(errors.New("runtime bundle entry count is outside bounds"))
	}
	files := make(map[string]*zip.File, len(reader.File))
	for index, file := range reader.File {
		if index > 0 && reader.File[index-1].Name >= file.Name {
			return cleanup(errors.New("runtime bundle entries are not strictly sorted"))
		}
		if !validArchivePath(file.Name) || file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return cleanup(fmt.Errorf("runtime bundle has invalid entry %q", file.Name))
		}
		if file.Method != zip.Store && file.Method != zip.Deflate {
			return cleanup(fmt.Errorf("runtime bundle entry %q uses unsupported compression", file.Name))
		}
		if file.Flags != 0x8 || file.Comment != "" || file.NonUTF8 || len(file.Extra) != 0 || file.ModifiedDate != 0 || file.ModifiedTime != 0 {
			return cleanup(fmt.Errorf("runtime bundle entry %q violates the v1 ZIP profile", file.Name))
		}
		if _, exists := files[file.Name]; exists {
			return cleanup(fmt.Errorf("runtime bundle has duplicate entry %q", file.Name))
		}
		files[file.Name] = file
	}
	manifestEntry, ok := files[manifestName]
	if !ok {
		return cleanup(errors.New("runtime bundle manifest is missing"))
	}
	if manifestEntry.CompressedSize64 > maximumManifestBytes {
		return cleanup(errors.New("runtime bundle manifest compressed size is outside bounds"))
	}
	manifest, err := readManifest(manifestEntry)
	if err != nil {
		return cleanup(err)
	}
	if len(files) != len(manifest.Files)+1 {
		return cleanup(errors.New("runtime bundle contains unlisted entries"))
	}
	var aggregate uint64
	var compressedAggregate uint64
	for index, expected := range manifest.Files {
		if index > 0 && manifest.Files[index-1].Path >= expected.Path {
			return cleanup(errors.New("runtime bundle manifest paths are not strictly sorted"))
		}
		if !validRuntimePath(expected.Path) || expected.Mode&^0o777 != 0 || expected.Mode&0o400 == 0 || expected.Size > maximumFileBytes || !validSHA256(expected.SHA256) {
			return cleanup(fmt.Errorf("runtime bundle manifest has invalid file %q", expected.Path))
		}
		if aggregate > maximumAggregateBytes-expected.Size {
			return cleanup(errors.New("runtime bundle aggregate size exceeds limit"))
		}
		aggregate += expected.Size
		entry, ok := files[expected.Path]
		if !ok {
			return cleanup(fmt.Errorf("runtime bundle entry %q is missing", expected.Path))
		}
		if entry.CompressedSize64 > maximumAggregateBytes || compressedAggregate > maximumAggregateBytes-entry.CompressedSize64 {
			return cleanup(errors.New("runtime bundle compressed aggregate size exceeds limit"))
		}
		compressedAggregate += entry.CompressedSize64
		if entry.UncompressedSize64 != expected.Size || uint32(entry.Mode().Perm()) != expected.Mode {
			return cleanup(fmt.Errorf("runtime bundle entry %q metadata mismatch", expected.Path))
		}
	}
	if err := validateRuntimeProfile(manifest.Files); err != nil {
		return cleanup(err)
	}
	return &Bundle{reader: reader, files: files, ordered: manifest.Files}, nil
}

func (bundle *Bundle) Close() error {
	return bundle.reader.Close()
}

func (bundle *Bundle) Verify() error {
	for _, expected := range bundle.ordered {
		entry := bundle.files[expected.Path]
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open runtime bundle entry %q: %w", expected.Path, err)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, io.LimitReader(reader, int64(expected.Size)+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return fmt.Errorf("hash runtime bundle entry %q: %w", expected.Path, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close runtime bundle entry %q: %w", expected.Path, closeErr)
		}
		if written != int64(expected.Size) || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("runtime bundle entry %q checksum mismatch", expected.Path)
		}
	}
	return nil
}

func (bundle *Bundle) Extract(root string) error {
	if err := bundle.Verify(); err != nil {
		return err
	}
	for _, expected := range bundle.ordered {
		destination := filepath.Join(root, filepath.FromSlash(expected.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("create runtime bundle directory: %w", err)
		}
		entryReader, err := bundle.files[expected.Path].Open()
		if err != nil {
			return fmt.Errorf("open runtime bundle entry %q: %w", expected.Path, err)
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(expected.Mode))
		if err != nil {
			_ = entryReader.Close()
			return fmt.Errorf("create runtime file %q: %w", expected.Path, err)
		}
		chmodErr := file.Chmod(fs.FileMode(expected.Mode))
		_, copyErr := io.Copy(file, io.LimitReader(entryReader, int64(expected.Size)+1))
		closeReadErr := entryReader.Close()
		syncErr := file.Sync()
		closeWriteErr := file.Close()
		if chmodErr != nil || copyErr != nil || closeReadErr != nil || syncErr != nil || closeWriteErr != nil {
			return fmt.Errorf("write runtime file %q: %w", expected.Path, errors.Join(chmodErr, copyErr, closeReadErr, syncErr, closeWriteErr))
		}
	}
	return syncTreeDirectories(root)
}

func (bundle *Bundle) VerifyExtracted(root string) error {
	expectedPaths := make(map[string]ManifestFile, len(bundle.ordered))
	for _, expected := range bundle.ordered {
		expectedPaths[filepath.FromSlash(expected.Path)] = expected
	}
	seen := make(map[string]struct{}, len(expectedPaths))
	runtimeRoot := filepath.Join(root, "runtime")
	err := filepath.WalkDir(runtimeRoot, func(value string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, value)
		if err != nil {
			return err
		}
		expected, ok := expectedPaths[relative]
		if !ok || entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("installed runtime has unexpected file %q", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != fs.FileMode(expected.Mode) || uint64(info.Size()) != expected.Size {
			return fmt.Errorf("installed runtime file %q metadata mismatch", relative)
		}
		file, err := os.Open(value)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(file, int64(expected.Size)+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("installed runtime file %q checksum mismatch", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return fmt.Errorf("verify installed runtime: %w", err)
	}
	if len(seen) != len(expectedPaths) {
		return errors.New("installed runtime is incomplete")
	}
	return nil
}

func readManifest(entry *zip.File) (Manifest, error) {
	if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maximumManifestBytes {
		return Manifest{}, errors.New("runtime bundle manifest size is outside bounds")
	}
	reader, err := entry.Open()
	if err != nil {
		return Manifest{}, fmt.Errorf("open runtime bundle manifest: %w", err)
	}
	defer reader.Close()
	value, err := io.ReadAll(io.LimitReader(reader, maximumManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read runtime bundle manifest: %w", err)
	}
	if len(value) > maximumManifestBytes {
		return Manifest{}, errors.New("runtime bundle manifest size is outside bounds")
	}
	if err := strictjson.RejectDuplicateKeys(value); err != nil {
		return Manifest{}, fmt.Errorf("decode runtime bundle manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode runtime bundle manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("runtime bundle manifest contains trailing JSON")
	}
	if manifest.FormatVersion != formatVersion || len(manifest.Files) == 0 || len(manifest.Files)+1 > maximumEntries {
		return Manifest{}, errors.New("runtime bundle manifest format or file count is unsupported")
	}
	return manifest, nil
}

func validArchivePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	return value == manifestName || validRuntimePath(value)
}

func validRuntimePath(value string) bool {
	return strings.HasPrefix(value, "runtime/") && len(value) > len("runtime/") && validArchivePathWithoutKind(value)
}

func validArchivePathWithoutKind(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !strings.HasPrefix(value, "/") && path.Clean(value) == value
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateArchiveEnd(executablePath string) error {
	file, err := os.Open(executablePath)
	if err != nil {
		return fmt.Errorf("open runtime bundle footer: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect runtime bundle footer: %w", err)
	}
	const maximumEOCDSearch = 65_557
	readSize := min(info.Size(), int64(maximumEOCDSearch))
	buffer := make([]byte, readSize)
	if _, err := file.ReadAt(buffer, info.Size()-readSize); err != nil {
		return fmt.Errorf("read runtime bundle footer: %w", err)
	}
	signature := []byte{'P', 'K', 0x05, 0x06}
	offset := bytes.LastIndex(buffer, signature)
	if offset < 0 || offset+22 != len(buffer) {
		return errors.New("runtime bundle EOCD is missing or not at end of executable")
	}
	eocd := buffer[offset:]
	if binary.LittleEndian.Uint16(eocd[8:10]) == 0xffff ||
		binary.LittleEndian.Uint16(eocd[10:12]) == 0xffff ||
		binary.LittleEndian.Uint32(eocd[12:16]) == 0xffffffff ||
		binary.LittleEndian.Uint32(eocd[16:20]) == 0xffffffff {
		return errors.New("runtime bundle ZIP64 is forbidden")
	}
	if binary.LittleEndian.Uint16(eocd[20:22]) != 0 {
		return errors.New("runtime bundle ZIP comment is forbidden")
	}
	if offset >= 20 && bytes.Equal(buffer[offset-20:offset-16], []byte{'P', 'K', 0x06, 0x07}) {
		return errors.New("runtime bundle ZIP64 locator is forbidden")
	}
	return nil
}

func syncTreeDirectories(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(value string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, value)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk extracted runtime directories: %w", err)
	}
	sort.Slice(directories, func(left, right int) bool { return len(directories[left]) > len(directories[right]) })
	for _, directoryPath := range directories {
		directory, err := os.Open(directoryPath)
		if err != nil {
			return fmt.Errorf("open runtime directory: %w", err)
		}
		syncErr := directory.Sync()
		closeErr := directory.Close()
		if syncErr != nil || closeErr != nil {
			return fmt.Errorf("sync runtime directory: %w", errors.Join(syncErr, closeErr))
		}
	}
	return nil
}
