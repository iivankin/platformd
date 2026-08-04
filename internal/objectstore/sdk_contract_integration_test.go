//go:build integration

package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestBoto3S3Contract(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1 with python3-boto3 installed")
	}
	fixture := startSDKContractServer(t)
	ctx := context.Background()
	command := exec.CommandContext(ctx, "python3", "-c", boto3ContractScript)
	command.Env = append(os.Environ(),
		"PLATFORMD_S3_ENDPOINT="+fixture.endpoint,
		"PLATFORMD_S3_BUCKET="+fixture.bucket,
		"PLATFORMD_S3_ACCESS_KEY="+fixture.accessKey,
		"PLATFORMD_S3_SECRET="+fixture.secret,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("boto3 S3 contract: %v\n%s", err, output)
	}
	if string(output) != "boto3 S3 contract passed\n" {
		t.Fatalf("unexpected boto3 output: %q", output)
	}
}

func TestLanceDBS3Contract(t *testing.T) {
	if os.Getenv("PLATFORMD_LANCEDB_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_LANCEDB_SDK_INTEGRATION=1 with lancedb installed for python3")
	}
	fixture := startSDKContractServer(t)
	command := exec.CommandContext(context.Background(), "python3", "-c", lanceDBContractScript)
	command.Env = append(os.Environ(),
		"PLATFORMD_S3_ENDPOINT="+fixture.endpoint,
		"PLATFORMD_S3_BUCKET="+fixture.bucket,
		"PLATFORMD_S3_ACCESS_KEY="+fixture.accessKey,
		"PLATFORMD_S3_SECRET="+fixture.secret,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("LanceDB S3 contract: %v\n%s", err, output)
	}
	if string(output) != "LanceDB S3 contract passed\n" {
		t.Fatalf("unexpected LanceDB output: %q", output)
	}
}

func TestMinIOStreamingS3Contract(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1")
	}
	fixture := startSDKContractServer(t)
	transport := &streamingRecordingTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
	client, err := minio.New(strings.TrimPrefix(fixture.endpoint, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(fixture.accessKey, fixture.secret, ""),
		Secure: false, Region: Region, Transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("minio-streaming-payload"), 6000)
	if _, err := client.PutObject(
		context.Background(), fixture.bucket, "minio-streamed.bin", bytes.NewReader(payload), int64(len(payload)),
		minio.PutObjectOptions{ContentEncoding: "aws-chunked", DisableMultipart: true},
	); err != nil {
		t.Fatal(err)
	}
	marker, encoding, decodedLength := transport.streamingHeaders()
	if marker != streamingPayloadHash || !headerContainsTokenForTest([]string{encoding}, "aws-chunked") || decodedLength != strconv.FormatInt(int64(len(payload)), 10) {
		t.Fatalf("MinIO did not send the expected streaming headers: hash=%q encoding=%q decoded=%q", marker, encoding, decodedLength)
	}
	object, err := client.GetObject(context.Background(), fixture.bucket, "minio-streamed.bin", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	stored, err := io.ReadAll(object)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatalf("MinIO streamed object size = %d", len(stored))
	}

	multipartPayload := bytes.Repeat([]byte("minio-multipart-payload"), 300_000)
	if _, err := client.PutObject(
		context.Background(), fixture.bucket, "minio-multipart.bin", bytes.NewReader(multipartPayload), int64(len(multipartPayload)),
		minio.PutObjectOptions{PartSize: 5 << 20},
	); err != nil {
		t.Fatal(err)
	}
	multipartObject, err := client.GetObject(context.Background(), fixture.bucket, "minio-multipart.bin", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer multipartObject.Close()
	storedMultipart, err := io.ReadAll(multipartObject)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedMultipart, multipartPayload) {
		t.Fatalf("MinIO multipart object size = %d", len(storedMultipart))
	}

	tamperingTransport := &streamingTamperingTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
	tamperingClient, err := minio.New(strings.TrimPrefix(fixture.endpoint, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(fixture.accessKey, fixture.secret, ""),
		Secure: false, Region: Region, Transport: tamperingTransport,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tamperingClient.PutObject(
		context.Background(), fixture.bucket, "tampered-stream.bin", bytes.NewReader(payload), int64(len(payload)),
		minio.PutObjectOptions{ContentEncoding: "aws-chunked", DisableMultipart: true},
	)
	if err == nil {
		t.Fatal("PUT with a tampered aws-chunked payload succeeded")
	}
	if !tamperingTransport.tampered {
		t.Fatal("test transport did not tamper with the aws-chunked payload")
	}
	if _, statErr := tamperingClient.StatObject(
		context.Background(), fixture.bucket, "tampered-stream.bin", minio.StatObjectOptions{},
	); statErr == nil || minio.ToErrorResponse(statErr).Code != "NoSuchKey" {
		t.Fatalf("tampered aws-chunked object was committed: %v", statErr)
	}
}

func TestRustFSRestoreReplacesObjectsAndMultipartUploads(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1")
	}
	fixture := startSDKContractServer(t)
	ctx := context.Background()
	core, err := minio.NewCore(strings.TrimPrefix(fixture.endpoint, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(fixture.accessKey, fixture.secret, ""),
		Region: Region,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("backup version")
	if _, err := fixture.application.Put(ctx, PutInput{
		StoreID: fixture.storeID, ObjectKey: "restored.txt", ContentType: "text/plain",
		Body: bytes.NewReader(original), BodySize: int64(len(original)), BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	export, err := fixture.application.BackupSnapshot(ctx, fixture.storeID)
	if err != nil {
		t.Fatal(err)
	}
	archive, readErr := io.ReadAll(export.Reader)
	closeErr := export.Reader.Close()
	export.Release()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}

	uploadID, err := core.NewMultipartUpload(ctx, fixture.bucket, "stale.bin", minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	partBody := []byte("stale multipart data")
	_, err = core.PutObjectPart(
		ctx, fixture.bucket, "stale.bin", uploadID, 1, bytes.NewReader(partBody), int64(len(partBody)),
		minio.PutObjectPartOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.Put(ctx, PutInput{
		StoreID: fixture.storeID, ObjectKey: "current-only.txt",
		Body: bytes.NewReader([]byte("must disappear")),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.Put(ctx, PutInput{
		StoreID: fixture.storeID, ObjectKey: "restored.txt", Body: bytes.NewReader([]byte("current version")),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.application.RestoreSnapshot(ctx, RestoreInput{
		StoreID: fixture.storeID, Archive: bytes.NewReader(archive),
		Actor: Actor{Kind: "system", ID: "integration-restore"},
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err := fixture.application.Object(ctx, fixture.storeID, "restored.txt")
	if err != nil {
		t.Fatal(err)
	}
	var restored bytes.Buffer
	if err := fixture.application.ReadRange(ctx, metadata, 0, metadata.Metadata.Size, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), original) || metadata.Metadata.ContentType != "text/plain" {
		t.Fatalf("restored object = %q metadata=%+v", restored.Bytes(), metadata.Metadata)
	}
	if _, err := fixture.application.Object(ctx, fixture.storeID, "current-only.txt"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("current-only object after restore = %v", err)
	}
	_, err = core.ListObjectParts(ctx, fixture.bucket, "stale.bin", uploadID, 0, 1000)
	if err == nil || minio.ToErrorResponse(err).Code != "NoSuchUpload" {
		t.Fatalf("multipart created before restore still exists: %v", err)
	}
}

func TestPublicStoreHeaderCannotReachSiblingBucket(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1")
	}
	fixture := startSDKContractServer(t)
	ctx := context.Background()
	sibling, err := fixture.application.Create(ctx, CreateInput{
		ProjectID: "sdk-project", Name: "backups", BucketName: "sdk-backups",
		Actor: Actor{Kind: "access", ID: "sdk", Email: "sdk@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.storage.ConfigureDataPlaneProject(ctx, fixture.projectID, strings.TrimPrefix(fixture.endpoint, "http://"), []DataPlaneStore{
		{
			StoreID: fixture.storeID, BucketName: fixture.bucket, AccessKey: fixture.accessKey,
			Secret: fixture.secret, Permission: "read_write", CORSOrigins: []string{},
		},
		{
			StoreID: sibling.Store.ID, BucketName: sibling.Store.BucketName, AccessKey: sibling.AccessKey,
			Secret: sibling.Secret, Permission: sibling.Credential.Permission, CORSOrigins: []string{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(fixture.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.Out.URL.Scheme = target.Scheme
		request.Out.URL.Host = target.Host
		request.Out.Host = request.In.Host
		request.Out.Header.Set("X-Platformd-Public-Store", fixture.storeID)
	}})
	defer proxy.Close()
	siblingClient, err := minio.New(strings.TrimPrefix(proxy.URL, "http://"), &minio.Options{
		Creds: credentials.NewStaticV4(sibling.AccessKey, sibling.Secret, ""), Region: Region,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = siblingClient.PutObject(
		ctx, sibling.Store.BucketName, "escape.txt", strings.NewReader("blocked"), 7,
		minio.PutObjectOptions{DisableMultipart: true},
	)
	if err == nil || minio.ToErrorResponse(err).Code != "NoSuchBucket" {
		t.Fatalf("sibling bucket through public route = %v", err)
	}
}

func TestRustFSBucketReconciliationRemovesOrphans(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1")
	}
	fixture := startSDKContractServer(t)
	ctx := context.Background()
	orphanID := "abcdefghijklmnopqrstuvwx"
	if err := fixture.storage.EnsureBucket(ctx, orphanID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.storage.Put(ctx, PutInput{
		StoreID: orphanID, ObjectKey: "orphan.txt", Body: strings.NewReader("orphan"),
		BodySize: 6, BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.storage.ReconcileBuckets(ctx, []string{fixture.storeID}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.storage.Put(ctx, PutInput{
		StoreID: orphanID, ObjectKey: "after.txt", Body: strings.NewReader("removed"),
		BodySize: 7, BodySizeKnown: true,
	}); err == nil {
		t.Fatal("orphan physical bucket survived reconciliation")
	}
}

func headerContainsTokenForTest(values []string, wanted string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), wanted) {
				return true
			}
		}
	}
	return false
}

type streamingRecordingTransport struct {
	base          http.RoundTripper
	mu            sync.Mutex
	marker        string
	encoding      string
	decodedLength string
}

func (transport *streamingRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/minio-streamed.bin") {
		transport.mu.Lock()
		transport.marker = request.Header.Get("X-Amz-Content-Sha256")
		transport.encoding = request.Header.Get("Content-Encoding")
		transport.decodedLength = request.Header.Get("X-Amz-Decoded-Content-Length")
		transport.mu.Unlock()
	}
	return transport.base.RoundTrip(request)
}

func (transport *streamingRecordingTransport) streamingHeaders() (string, string, string) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.marker, transport.encoding, transport.decodedLength
}

type streamingTamperingTransport struct {
	base     http.RoundTripper
	tampered bool
}

func (transport *streamingTamperingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/tampered-stream.bin") {
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		chunkHeaderEnd := bytes.Index(encoded, []byte("\r\n"))
		if chunkHeaderEnd < 0 || chunkHeaderEnd+2 >= len(encoded) {
			return nil, errors.New("aws-chunked request is missing its first chunk")
		}
		encoded[chunkHeaderEnd+2] ^= 1
		request.Body = io.NopCloser(bytes.NewReader(encoded))
		transport.tampered = true
	}
	return transport.base.RoundTrip(request)
}

type sdkContractFixture struct {
	endpoint    string
	bucket      string
	accessKey   string
	secret      string
	storeID     string
	projectID   string
	application *Application
	storage     *SidecarClient
}

func TestLargestObjectsSearch(t *testing.T) {
	fixture := startSDKContractServer(t)
	ctx := context.Background()
	for size := 1; size <= 12; size++ {
		body := bytes.Repeat([]byte{byte(size)}, size)
		if _, err := fixture.application.Put(ctx, PutInput{
			StoreID: fixture.storeID, ObjectKey: "object-" + strconv.Itoa(size),
			Body: bytes.NewReader(body), BodySize: int64(len(body)), BodySizeKnown: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	search, err := fixture.application.StartLargestObjects(ctx, fixture.projectID, fixture.storeID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for search.Status == LargestObjectsRunning || search.Status == LargestObjectsCancelling {
		if time.Now().After(deadline) {
			t.Fatalf("largest object search timed out: %+v", search)
		}
		time.Sleep(20 * time.Millisecond)
		search, err = fixture.application.LargestObjects(ctx, fixture.projectID, fixture.storeID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if search.Status != LargestObjectsComplete || search.ScannedObjects != 12 || len(search.Objects) != 10 {
		t.Fatalf("largest object search = %+v", search)
	}
	if search.Objects[0].Size != 12 || search.Objects[9].Size != 3 {
		t.Fatalf("largest object ordering = %+v", search.Objects)
	}
}

var sidecarBuild struct {
	once   sync.Once
	binary string
	output []byte
	err    error
}

func releaseSidecarBinary() (string, []byte, error) {
	sidecarBuild.once.Do(func() {
		if configured := os.Getenv("PLATFORMD_OBJECTSTORE_SIDECAR_BINARY"); configured != "" {
			sidecarBuild.binary, sidecarBuild.err = filepath.Abs(configured)
			return
		}
		sidecarRoot := filepath.Join("sidecar")
		build := exec.Command("cargo", "build", "--release", "--locked")
		build.Dir = sidecarRoot
		sidecarBuild.output, sidecarBuild.err = build.CombinedOutput()
		if sidecarBuild.err == nil {
			sidecarBuild.binary, sidecarBuild.err = filepath.Abs(
				filepath.Join(sidecarRoot, "target", "release", "platformd-objectstore"),
			)
		}
	})
	return sidecarBuild.binary, sidecarBuild.output, sidecarBuild.err
}

func startSDKContractServer(t testing.TB) sdkContractFixture {
	t.Helper()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "sdk-project", Name: "sdk", AuditEventID: "sdk-project-audit", ActorID: "sdk",
		ActorEmail: "sdk@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	binary, output, buildErr := releaseSidecarBinary()
	if buildErr != nil {
		t.Fatalf("build object store sidecar: %v\n%s", buildErr, output)
	}
	socketRoot, err := os.MkdirTemp("/tmp", "platformd-objectstore-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	process, storage, err := StartSidecar(
		ctx, binary, filepath.Join(t.TempDir(), "objects"), filepath.Join(socketRoot, "socket"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close() })
	application, err := NewApplication(store, storage, master, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.Create(ctx, CreateInput{
		ProjectID: "sdk-project", Name: "objects", BucketName: "sdk-bucket",
		Actor: Actor{Kind: "access", ID: "sdk", Email: "sdk@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddress := probe.Addr().String()
	_ = probe.Close()
	if err := storage.ConfigureDataPlaneProject(ctx, "sdk-project", listenAddress, []DataPlaneStore{{
		StoreID: created.Store.ID, BucketName: created.Store.BucketName,
		AccessKey: created.AccessKey, Secret: created.Secret,
		Permission: created.Credential.Permission, CORSOrigins: created.Store.CORSOrigins,
	}}); err != nil {
		t.Fatal(err)
	}

	return sdkContractFixture{
		endpoint: "http://" + listenAddress, bucket: created.Store.BucketName,
		accessKey: created.AccessKey, secret: created.Secret,
		storeID: created.Store.ID, projectID: "sdk-project", application: application, storage: storage,
	}
}

const boto3ContractScript = `
import os
import urllib.error
import urllib.request
import urllib.parse

import boto3
from botocore.client import Config
from botocore.exceptions import ClientError

endpoint = os.environ["PLATFORMD_S3_ENDPOINT"]
bucket = os.environ["PLATFORMD_S3_BUCKET"]
client = boto3.client(
    "s3",
    endpoint_url=endpoint,
    aws_access_key_id=os.environ["PLATFORMD_S3_ACCESS_KEY"],
    aws_secret_access_key=os.environ["PLATFORMD_S3_SECRET"],
    region_name="us-east-1",
    config=Config(signature_version="s3v4", s3={"addressing_style": "path"}),
)

unsigned_headers = {}
def add_put_operation_id(request, **kwargs):
    parsed = urllib.parse.urlsplit(request.url)
    query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    if not any(name == "x-id" for name, _ in query):
        query.append(("x-id", "PutObject"))
    request.url = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))

def capture_unsigned_payload(request, **kwargs):
    value = request.headers.get("X-Amz-Content-SHA256")
    unsigned_headers["payload_hash"] = value.decode() if isinstance(value, bytes) else value
    unsigned_headers["operation_id"] = urllib.parse.parse_qs(urllib.parse.urlsplit(request.url).query).get("x-id")

unsigned_client = boto3.client(
    "s3",
    endpoint_url=endpoint,
    aws_access_key_id=os.environ["PLATFORMD_S3_ACCESS_KEY"],
    aws_secret_access_key=os.environ["PLATFORMD_S3_SECRET"],
    region_name="us-east-1",
    config=Config(
        signature_version="s3v4",
        request_checksum_calculation="when_required",
        s3={"addressing_style": "path", "payload_signing_enabled": False},
    ),
)
unsigned_client.meta.events.register("before-sign.s3.PutObject", add_put_operation_id)
unsigned_client.meta.events.register("before-send.s3.PutObject", capture_unsigned_payload)

client.head_bucket(Bucket=bucket)
assert client.get_bucket_location(Bucket=bucket)["LocationConstraint"] is None
payload = b"hello from boto3"
unsigned_client.put_object(Bucket=bucket, Key="unsigned-client.bin", Body=b"unsigned from boto3")
assert unsigned_headers["payload_hash"] == "UNSIGNED-PAYLOAD"
assert unsigned_headers["operation_id"] == ["PutObject"]
put = client.put_object(
    Bucket=bucket,
    Key="folder/hello world.txt",
    Body=payload,
    ContentType="text/plain",
    Metadata={"color": "blue"},
)
assert put["ETag"].startswith('"')
head = client.head_object(Bucket=bucket, Key="folder/hello world.txt")
assert head["ContentLength"] == len(payload) and head["Metadata"] == {"color": "blue"}
try:
    client.put_object(Bucket=bucket, Key="folder/hello world.txt", Body=b"conflict", IfNoneMatch="*")
    raise AssertionError("conditional create overwrote an object")
except ClientError as error:
    assert error.response["Error"]["Code"] == "PreconditionFailed"
updated = client.put_object(
    Bucket=bucket,
    Key="folder/hello world.txt",
    Body=payload,
    Metadata={"color": "blue"},
    IfMatch=put["ETag"],
)
assert updated["ETag"] == put["ETag"]
head = client.head_object(Bucket=bucket, Key="folder/hello world.txt")
assert head["ContentLength"] == len(payload) and head["Metadata"] == {"color": "blue"}
get = client.get_object(Bucket=bucket, Key="folder/hello world.txt", Range="bytes=6-9")
assert get["Body"].read() == b"from" and get["Metadata"] == {"color": "blue"}
listed = client.list_objects_v2(Bucket=bucket, Prefix="folder/", MaxKeys=1)
assert listed["KeyCount"] == 1 and listed["Contents"][0]["Key"] == "folder/hello world.txt"

client.put_object(Bucket=bucket, Key="catalog/alpha.lance/_versions/1.manifest", Body=b"alpha")
client.put_object(Bucket=bucket, Key="catalog/beta.lance/_versions/1.manifest", Body=b"beta")
client.put_object(Bucket=bucket, Key="catalog/root.txt", Body=b"root")
directories = client.list_objects_v2(Bucket=bucket, Prefix="catalog/", Delimiter="/", MaxKeys=2)
assert directories["KeyCount"] == 2 and directories["IsTruncated"]
assert [item["Prefix"] for item in directories["CommonPrefixes"]] == ["catalog/alpha.lance/", "catalog/beta.lance/"]
remaining = client.list_objects_v2(
    Bucket=bucket,
    Prefix="catalog/",
    Delimiter="/",
    MaxKeys=2,
    ContinuationToken=directories["NextContinuationToken"],
)
assert remaining["KeyCount"] == 1 and remaining["Contents"][0]["Key"] == "catalog/root.txt"

copied = client.copy_object(Bucket=bucket, Key="copied.txt", CopySource={"Bucket": bucket, "Key": "folder/hello world.txt"})
assert copied["CopyObjectResult"]["ETag"] == put["ETag"]
copied_get = client.get_object(Bucket=bucket, Key="copied.txt")
assert copied_get["Body"].read() == payload and copied_get["Metadata"] == {"color": "blue"}
client.copy_object(
    Bucket=bucket,
    Key="copied-replaced.txt",
    CopySource={"Bucket": bucket, "Key": "folder/hello world.txt"},
    MetadataDirective="REPLACE",
    Metadata={"shape": "round"},
    ContentType="application/octet-stream",
)
replaced = client.get_object(Bucket=bucket, Key="copied-replaced.txt")
assert replaced["Body"].read() == payload and replaced["Metadata"] == {"shape": "round"}
assert replaced["ContentType"] == "application/octet-stream"

presigned_get = client.generate_presigned_url("get_object", Params={"Bucket": bucket, "Key": "folder/hello world.txt"}, ExpiresIn=60)
assert urllib.request.urlopen(presigned_get).read() == payload
presigned_put = client.generate_presigned_url("put_object", Params={"Bucket": bucket, "Key": "presigned.txt"}, ExpiresIn=60)
urllib.request.urlopen(urllib.request.Request(presigned_put, data=b"presigned", method="PUT")).read()
assert client.get_object(Bucket=bucket, Key="presigned.txt")["Body"].read() == b"presigned"

created = client.create_multipart_upload(Bucket=bucket, Key="multipart.bin", ContentType="application/octet-stream")
upload_id = created["UploadId"]
first = client.upload_part(Bucket=bucket, Key="multipart.bin", UploadId=upload_id, PartNumber=1, Body=b"a" * (5 * 1024 * 1024))
second = client.upload_part(Bucket=bucket, Key="multipart.bin", UploadId=upload_id, PartNumber=2, Body=b"tail")
parts = client.list_parts(Bucket=bucket, Key="multipart.bin", UploadId=upload_id, MaxParts=1)
assert parts["IsTruncated"] and parts["Parts"][0]["ETag"] == first["ETag"]
completed = client.complete_multipart_upload(
    Bucket=bucket,
    Key="multipart.bin",
    UploadId=upload_id,
    MultipartUpload={"Parts": [
        {"PartNumber": 1, "ETag": first["ETag"]},
        {"PartNumber": 2, "ETag": second["ETag"]},
    ]},
)
assert completed["ETag"].startswith('"')
assert client.get_object(Bucket=bucket, Key="multipart.bin", Range="bytes=5242878-5242883")["Body"].read() == b"aatail"

aborted = client.create_multipart_upload(Bucket=bucket, Key="abort.bin")
client.abort_multipart_upload(Bucket=bucket, Key="abort.bin", UploadId=aborted["UploadId"])

try:
    urllib.request.urlopen(endpoint + "/" + bucket + "/folder/hello%20world.txt")
    raise AssertionError("anonymous object read was accepted")
except urllib.error.HTTPError as error:
    assert error.code == 403

try:
    client.list_objects_v2(Bucket="another-bucket")
    raise AssertionError("another bucket was accepted")
except ClientError as error:
    assert error.response["Error"]["Code"] == "NoSuchBucket"

deleted = client.delete_objects(Bucket=bucket, Delete={"Objects": [
    {"Key": "folder/hello world.txt"},
    {"Key": "presigned.txt"},
    {"Key": "multipart.bin"},
    {"Key": "copied.txt"},
    {"Key": "catalog/alpha.lance/_versions/1.manifest"},
    {"Key": "catalog/beta.lance/_versions/1.manifest"},
    {"Key": "catalog/root.txt"},
    {"Key": "unsigned-client.bin"},
    {"Key": "already-missing"},
]})
assert len(deleted["Deleted"]) == 9
print("boto3 S3 contract passed")
`

const lanceDBContractScript = `
import os
from datetime import timedelta

import lancedb
from lancedb.index import BTree

endpoint = os.environ["PLATFORMD_S3_ENDPOINT"]
bucket = os.environ["PLATFORMD_S3_BUCKET"]
database = lancedb.connect(
    f"s3://{bucket}/integration",
    storage_options={
        "aws_access_key_id": os.environ["PLATFORMD_S3_ACCESS_KEY"],
        "aws_secret_access_key": os.environ["PLATFORMD_S3_SECRET"],
        "aws_region": "us-east-1",
        "aws_endpoint": endpoint,
        "allow_http": "true",
        "aws_virtual_hosted_style_request": "false",
    },
)

assert database.list_tables().tables == []
table = database.create_table("vectors", data=[
    {"id": 1, "text": "alpha", "vector": [1.0, 0.0]},
    {"id": 2, "text": "beta", "vector": [0.0, 1.0]},
])
table.add([{"id": 3, "text": "gamma", "vector": [0.9, 0.1]}])
assert database.list_tables().tables == ["vectors"]
result = table.search([1.0, 0.0]).limit(1).to_list()
assert result[0]["id"] == 1

table.create_index("id", config=BTree())
filtered = table.search().where("id = 3").to_list()
assert len(filtered) == 1 and filtered[0]["text"] == "gamma"
table.optimize(cleanup_older_than=timedelta(0), delete_unverified=True)

database.drop_table("vectors")
assert database.list_tables().tables == []
print("LanceDB S3 contract passed")
`
