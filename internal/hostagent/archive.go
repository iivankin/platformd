package hostagent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/state"
)

type ArchiveStore struct {
	*RemoteStore
	parentURL string
	hostToken string
	imageRoot string
}

func NewArchiveStore(store *RemoteStore, parentURL, hostToken, imageRoot string) *ArchiveStore {
	return &ArchiveStore{RemoteStore: store, parentURL: parentURL, hostToken: hostToken, imageRoot: imageRoot}
}

func (store *ArchiveStore) LatestReusableProductionRevision(ctx context.Context, serviceID string) (state.ImageRevision, error) {
	revision, err := store.RemoteStore.LatestReusableProductionRevision(ctx, serviceID)
	if err != nil {
		return state.ImageRevision{}, err
	}
	local, err := DownloadArchive(ctx, store.parentURL, store.hostToken, revision.ID, store.imageRoot)
	if err != nil {
		return state.ImageRevision{}, err
	}
	revision.ArchivePath = local
	return revision, nil
}

func DownloadArchive(ctx context.Context, parentURL, hostToken, revisionID, imageRoot string) (string, error) {
	if err := os.MkdirAll(imageRoot, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(imageRoot, revisionID+".oci")
	if info, err := os.Stat(destination); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return destination, nil
	}
	endpoint, err := parentHTTPURL(parentURL, hostconn.ImagePathPrefix+revisionID)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+hostToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download image archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download image archive: status %d", response.StatusCode)
	}
	temporary, err := os.CreateTemp(imageRoot, ".image-")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, response.Body); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	return destination, nil
}
