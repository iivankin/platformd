package daemon

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/state"
)

const maximumExpandedRepositoryBytes = 2 << 30

type githubBuildEngine interface {
	Build(context.Context, containerengine.BuildRequest) (containerengine.Image, error)
}

type githubSourceResolver struct {
	github        *githubapp.Application
	engine        githubBuildEngine
	generatedRoot string
}

func (resolver githubSourceResolver) Resolve(
	ctx context.Context,
	desired state.ServiceDesired,
	deploymentID string,
	revisionOverride string,
	log io.Writer,
	force bool,
) (deployment.SourceResolution, error) {
	if resolver.github == nil {
		return deployment.SourceResolution{}, errors.New("GitHub App is not configured")
	}
	github := desired.Snapshot.Source.GitHub
	if github == nil {
		return deployment.SourceResolution{}, errors.New("GitHub source settings are missing")
	}
	revision := revisionOverride
	if revision == "" {
		revision = github.Revision
	}
	if revision == "" {
		revision = github.Branch
	}
	commit, err := resolver.github.Commit(ctx, github.RepositoryID, revision)
	if err != nil {
		return deployment.SourceResolution{}, fmt.Errorf("resolve GitHub commit: %w", err)
	}
	result := deployment.SourceResolution{Revision: commit.SHA, CommitMessage: commit.Message}
	_, _ = fmt.Fprintf(log, "Resolved %s at %s\n", github.Repository, commit.SHA)
	if github.WaitForCI && !force {
		checks, err := resolver.github.Checks(ctx, github.RepositoryID, commit.SHA)
		if err != nil {
			return result, fmt.Errorf("load GitHub checks: %w", err)
		}
		switch checks {
		case githubapp.ChecksPending:
			_, _ = io.WriteString(log, "Waiting for GitHub CI checks\n")
			return result, deployment.ErrSourceChecksPending
		case githubapp.ChecksFailed:
			return result, &deployment.SourceSkippedError{Reason: "GitHub CI checks did not pass"}
		}
	}

	workRoot, err := os.MkdirTemp(resolver.generatedRoot, "github-build-")
	if err != nil {
		return result, fmt.Errorf("create GitHub build workspace: %w", err)
	}
	defer os.RemoveAll(workRoot)
	archivePath := filepath.Join(workRoot, "source.tar.gz")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return result, err
	}
	_, _ = io.WriteString(log, "Downloading repository archive\n")
	if err := resolver.github.DownloadArchive(ctx, github.RepositoryID, commit.SHA, archive); err != nil {
		archive.Close()
		return result, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		archive.Close()
		return result, err
	}
	sourceRoot := filepath.Join(workRoot, "source")
	if err := extractGitHubArchive(archive, sourceRoot); err != nil {
		archive.Close()
		return result, err
	}
	if err := archive.Close(); err != nil {
		return result, err
	}
	contextPath := filepath.Join(sourceRoot, filepath.FromSlash(github.ContextPath))
	dockerfilePath := filepath.Join(sourceRoot, filepath.FromSlash(github.DockerfilePath))
	if !pathInside(sourceRoot, contextPath) || !pathInside(sourceRoot, dockerfilePath) {
		return result, errors.New("GitHub build paths escape the repository")
	}
	if info, err := os.Stat(contextPath); err != nil || !info.IsDir() {
		return result, errors.New("GitHub build context does not exist")
	}
	if info, err := os.Stat(dockerfilePath); err != nil || !info.Mode().IsRegular() {
		return result, errors.New("GitHub Dockerfile does not exist")
	}
	result.ImageReference = "localhost/platformd-build/" + desired.ID + ":" + commit.SHA
	_, _ = fmt.Fprintf(log, "Building %s from %s\n", result.ImageReference, github.DockerfilePath)
	image, err := resolver.engine.Build(ctx, containerengine.BuildRequest{
		ContextDirectory: contextPath,
		Dockerfile:       dockerfilePath,
		Reference:        result.ImageReference,
		Log:              log,
	})
	result.Image = image
	if err != nil {
		return result, err
	}
	_, _ = fmt.Fprintf(log, "Built image %s\n", image.Digest)
	return result, nil
}

func extractGitHubArchive(source io.Reader, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	compressed, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("open GitHub archive: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	var expanded int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read GitHub archive: %w", err)
		}
		name := stripArchiveRoot(header.Name)
		if name == "" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if !pathInside(destination, target) {
			return errors.New("GitHub archive contains an unsafe path")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			expanded += header.Size
			if expanded > maximumExpandedRepositoryBytes {
				return errors.New("expanded GitHub repository exceeds 2 GiB")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if header.FileInfo().Mode()&0o111 != 0 {
				mode = 0o700
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := createSafeSymlink(destination, target, header.Linkname); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
		default:
			return fmt.Errorf("GitHub archive contains unsupported entry type %d", header.Typeflag)
		}
	}
}

func stripArchiveRoot(value string) string {
	cleaned := filepath.ToSlash(filepath.Clean(value))
	parts := strings.Split(cleaned, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[1:], "/")
}

func pathInside(root, value string) bool {
	relative, err := filepath.Rel(root, value)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func createSafeSymlink(root, target, link string) error {
	if filepath.IsAbs(link) || !pathInside(root, filepath.Clean(filepath.Join(filepath.Dir(target), link))) {
		return errors.New("GitHub archive contains an unsafe symlink")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.Symlink(link, target)
}
