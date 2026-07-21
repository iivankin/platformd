package remotes3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// PurgePrefix permanently removes every object version, delete marker and
// unfinished multipart upload below prefix. Plain Delete is not sufficient on
// versioned S3 providers because it only creates another delete marker.
func (client *Client) PurgePrefix(ctx context.Context, prefix string) error {
	if err := validatePurgePrefix(prefix); err != nil {
		return err
	}
	if err := client.purgeVersions(ctx, prefix); err != nil {
		return fmt.Errorf("purge remote S3 object versions: %w", err)
	}
	if err := client.purgeMultipartUploads(ctx, prefix); err != nil {
		return fmt.Errorf("purge remote S3 multipart uploads: %w", err)
	}
	return nil
}

type objectVersion struct {
	key       string
	versionID string
}

type versionPage struct {
	versions            []objectVersion
	nextKeyMarker       string
	nextVersionIDMarker string
}

func (client *Client) purgeVersions(ctx context.Context, prefix string) error {
	keyMarker := ""
	versionIDMarker := ""
	versions := make([]objectVersion, 0)
	for {
		page, err := client.listVersions(ctx, prefix, keyMarker, versionIDMarker)
		if err != nil {
			return err
		}
		versions = append(versions, page.versions...)
		if page.nextKeyMarker == "" {
			break
		}
		if page.nextKeyMarker == keyMarker && page.nextVersionIDMarker == versionIDMarker {
			return errors.New("remote S3 truncated version list omitted new markers")
		}
		keyMarker, versionIDMarker = page.nextKeyMarker, page.nextVersionIDMarker
	}
	// Continuation markers may identify a concrete object version. Keep the
	// listing stable until every page has been collected, then delete it.
	for _, version := range versions {
		if err := client.deleteVersion(ctx, version); err != nil {
			return err
		}
	}
	return nil
}

func (client *Client) listVersions(ctx context.Context, prefix, keyMarker, versionIDMarker string) (versionPage, error) {
	query := url.Values{"versions": []string{""}, "prefix": []string{prefix}}
	if keyMarker != "" {
		query.Set("key-marker", keyMarker)
	}
	if versionIDMarker != "" {
		query.Set("version-id-marker", versionIDMarker)
	}
	request, err := client.request(ctx, http.MethodGet, "", query, nil)
	if err != nil {
		return versionPage{}, err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return versionPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return versionPage{}, remoteError(response)
	}
	var result struct {
		IsTruncated         bool
		NextKeyMarker       string
		NextVersionIDMarker string `xml:"NextVersionIdMarker"`
		Versions            []struct {
			Key       string
			VersionID string `xml:"VersionId"`
		} `xml:"Version"`
		DeleteMarkers []struct {
			Key       string
			VersionID string `xml:"VersionId"`
		} `xml:"DeleteMarker"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(&result); err != nil {
		return versionPage{}, fmt.Errorf("decode remote S3 version list: %w", err)
	}
	page := versionPage{versions: make([]objectVersion, 0, len(result.Versions)+len(result.DeleteMarkers))}
	appendVersion := func(key, versionID string) error {
		if !strings.HasPrefix(key, prefix) || versionID == "" || strings.ContainsAny(versionID, "\r\n\x00") {
			return errors.New("remote S3 version list contains an invalid entry")
		}
		page.versions = append(page.versions, objectVersion{key: key, versionID: versionID})
		return nil
	}
	for _, version := range result.Versions {
		if err := appendVersion(version.Key, version.VersionID); err != nil {
			return versionPage{}, err
		}
	}
	for _, marker := range result.DeleteMarkers {
		if err := appendVersion(marker.Key, marker.VersionID); err != nil {
			return versionPage{}, err
		}
	}
	if result.IsTruncated {
		if result.NextKeyMarker == "" {
			return versionPage{}, errors.New("remote S3 truncated version list omitted next key marker")
		}
		page.nextKeyMarker = result.NextKeyMarker
		page.nextVersionIDMarker = result.NextVersionIDMarker
	}
	return page, nil
}

func (client *Client) deleteVersion(ctx context.Context, version objectVersion) error {
	request, err := client.request(ctx, http.MethodDelete, version.key, url.Values{"versionId": []string{version.versionID}}, nil)
	if err != nil {
		return err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return remoteError(response)
	}
	return drain(response)
}

type multipartUpload struct {
	key      string
	uploadID string
}

func (client *Client) purgeMultipartUploads(ctx context.Context, prefix string) error {
	keyMarker := ""
	uploadIDMarker := ""
	uploads := make([]multipartUpload, 0)
	for {
		query := url.Values{"uploads": []string{""}, "prefix": []string{prefix}}
		if keyMarker != "" {
			query.Set("key-marker", keyMarker)
		}
		if uploadIDMarker != "" {
			query.Set("upload-id-marker", uploadIDMarker)
		}
		request, err := client.request(ctx, http.MethodGet, "", query, nil)
		if err != nil {
			return err
		}
		response, err := client.do(request, emptyPayloadHash())
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			remoteErr := remoteError(response)
			_ = response.Body.Close()
			return remoteErr
		}
		var result struct {
			IsTruncated        bool
			NextKeyMarker      string
			NextUploadIDMarker string `xml:"NextUploadIdMarker"`
			Uploads            []struct {
				Key      string
				UploadID string `xml:"UploadId"`
			} `xml:"Upload"`
		}
		decodeErr := xml.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&result)
		closeErr := response.Body.Close()
		if decodeErr != nil || closeErr != nil {
			return errors.Join(decodeErr, closeErr)
		}
		for _, upload := range result.Uploads {
			if !strings.HasPrefix(upload.Key, prefix) || upload.UploadID == "" || strings.ContainsAny(upload.UploadID, "\r\n\x00") {
				return errors.New("remote S3 multipart list contains an invalid entry")
			}
			uploads = append(uploads, multipartUpload{key: upload.Key, uploadID: upload.UploadID})
		}
		if !result.IsTruncated {
			break
		}
		if result.NextKeyMarker == "" || result.NextKeyMarker == keyMarker && result.NextUploadIDMarker == uploadIDMarker {
			return errors.New("remote S3 truncated multipart list omitted new markers")
		}
		keyMarker, uploadIDMarker = result.NextKeyMarker, result.NextUploadIDMarker
	}
	for _, upload := range uploads {
		if err := client.abortMultipartUpload(ctx, upload); err != nil {
			return err
		}
	}
	return nil
}

func (client *Client) abortMultipartUpload(ctx context.Context, upload multipartUpload) error {
	request, err := client.request(ctx, http.MethodDelete, upload.key, url.Values{"uploadId": []string{upload.uploadID}}, nil)
	if err != nil {
		return err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return remoteError(response)
	}
	return drain(response)
}

func validatePurgePrefix(prefix string) error {
	if !strings.HasSuffix(prefix, "/") || validateKey(strings.TrimSuffix(prefix, "/")) != nil {
		return errors.New("remote S3 purge prefix must be a non-root object directory")
	}
	return nil
}
