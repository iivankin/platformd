package remotes3

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

func TestPurgePrefixDeletesVersionsMarkersAndMultipartUploads(t *testing.T) {
	t.Parallel()
	var mutex sync.Mutex
	deleted := make([]string, 0)
	listedVersions := 0
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		switch {
		case request.Method == http.MethodGet && query.Has("versions"):
			mutex.Lock()
			listedVersions++
			page := listedVersions
			mutex.Unlock()
			response.Header().Set("Content-Type", "application/xml")
			if page == 1 {
				_, _ = fmt.Fprint(response, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>root/resources/postgres/db/generations/b</NextKeyMarker><NextVersionIdMarker>v2</NextVersionIdMarker><Version><Key>root/resources/postgres/db/generations/a</Key><VersionId>v1</VersionId></Version><DeleteMarker><Key>root/resources/postgres/db/generations/b</Key><VersionId>v2</VersionId></DeleteMarker></ListVersionsResult>`)
				return
			}
			if query.Get("key-marker") != "root/resources/postgres/db/generations/b" || query.Get("version-id-marker") != "v2" {
				http.Error(response, "missing version pagination markers", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(response, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>root/resources/postgres/db/generations/c</Key><VersionId>null</VersionId></Version></ListVersionsResult>`)
		case request.Method == http.MethodGet && query.Has("uploads"):
			response.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(response, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>root/resources/postgres/db/generations/incomplete</Key><UploadId>upload-1</UploadId></Upload></ListMultipartUploadsResult>`)
		case request.Method == http.MethodDelete:
			mutex.Lock()
			deleted = append(deleted, request.URL.EscapedPath()+"?"+request.URL.RawQuery)
			mutex.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			http.Error(response, "unexpected request", http.StatusBadRequest)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client, err := New(Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "test-bucket", Prefix: "root",
		AccessKeyID: remoteTestAccessKey, SecretAccessKey: remoteTestSecret, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PurgePrefix(context.Background(), client.Key("resources/postgres/db/generations/")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/test-bucket/root/resources/postgres/db/generations/a?versionId=v1",
		"/test-bucket/root/resources/postgres/db/generations/b?versionId=v2",
		"/test-bucket/root/resources/postgres/db/generations/c?versionId=null",
		"/test-bucket/root/resources/postgres/db/generations/incomplete?uploadId=upload-1",
	}
	for index := range want {
		parsed, parseErr := url.Parse(want[index])
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		want[index] = parsed.EscapedPath() + "?" + parsed.RawQuery
	}
	mutex.Lock()
	defer mutex.Unlock()
	if !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted requests = %#v, want %#v", deleted, want)
	}
}

func TestPurgePrefixRejectsBucketRoot(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Endpoint: "https://s3.example.com", Region: "us-east-1", Bucket: "test-bucket",
		AccessKeyID: remoteTestAccessKey, SecretAccessKey: remoteTestSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "root", "/"} {
		if err := client.PurgePrefix(context.Background(), prefix); err == nil {
			t.Fatalf("accepted unsafe purge prefix %q", prefix)
		}
	}
}
