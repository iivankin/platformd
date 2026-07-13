//go:build integration

package objectstore

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestBoto3S3Contract(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1 with python3-boto3 installed")
	}
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "sdk-project", Name: "sdk", AuditEventID: "sdk-project-audit", ActorID: "sdk",
		ActorEmail: "sdk@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	payloads, err := NewPayloadStore(filepath.Join(t.TempDir(), "objects"), master, nil)
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, master, nil, nil)
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
	handler, err := NewHTTPHandler(HTTPConfig{
		Application: application,
		LookupHost: func(context.Context, string) (state.ObjectStore, error) {
			return created.Store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	command := exec.CommandContext(ctx, "python3", "-c", boto3ContractScript)
	command.Env = append(os.Environ(),
		"PLATFORMD_S3_ENDPOINT=http://127.0.0.1:9000",
		"PLATFORMD_S3_BUCKET="+created.Store.BucketName,
		"PLATFORMD_S3_ACCESS_KEY="+created.AccessKey,
		"PLATFORMD_S3_SECRET="+created.Secret,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("boto3 S3 contract: %v\n%s", err, output)
	}
	if string(output) != "boto3 S3 contract passed\n" {
		t.Fatalf("unexpected boto3 output: %q", output)
	}
}

const boto3ContractScript = `
import os
import urllib.error
import urllib.request

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

client.head_bucket(Bucket=bucket)
payload = b"hello from boto3"
put = client.put_object(Bucket=bucket, Key="folder/hello world.txt", Body=payload, ContentType="text/plain")
assert put["ETag"].startswith('"')
assert client.head_object(Bucket=bucket, Key="folder/hello world.txt")["ContentLength"] == len(payload)
assert client.get_object(Bucket=bucket, Key="folder/hello world.txt", Range="bytes=6-9")["Body"].read() == b"from"
listed = client.list_objects_v2(Bucket=bucket, Prefix="folder/", MaxKeys=1)
assert listed["KeyCount"] == 1 and listed["Contents"][0]["Key"] == "folder/hello world.txt"

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

client.delete_object(Bucket=bucket, Key="folder/hello world.txt")
client.delete_object(Bucket=bucket, Key="presigned.txt")
client.delete_object(Bucket=bucket, Key="multipart.bin")
print("boto3 S3 contract passed")
`
