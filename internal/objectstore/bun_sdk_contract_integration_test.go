//go:build integration

package objectstore

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestBunS3Contract(t *testing.T) {
	if os.Getenv("PLATFORMD_S3_SDK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_S3_SDK_INTEGRATION=1 with Bun installed")
	}
	fixture := startSDKContractServer(t)
	command := exec.CommandContext(context.Background(), "bun", "-e", bunS3ContractScript)
	command.Env = append(os.Environ(),
		"PLATFORMD_S3_ENDPOINT="+fixture.endpoint,
		"PLATFORMD_S3_BUCKET="+fixture.bucket,
		"PLATFORMD_S3_ACCESS_KEY="+fixture.accessKey,
		"PLATFORMD_S3_SECRET="+fixture.secret,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Bun.S3 contract: %v\n%s", err, output)
	}
	if string(output) != "Bun.S3 contract passed\n" {
		t.Fatalf("unexpected Bun.S3 output: %q", output)
	}
}

const bunS3ContractScript = `
const endpoint = process.env.PLATFORMD_S3_ENDPOINT;
const bucket = process.env.PLATFORMD_S3_BUCKET;
const client = new Bun.S3Client({
  endpoint,
  bucket,
  accessKeyId: process.env.PLATFORMD_S3_ACCESS_KEY,
  secretAccessKey: process.env.PLATFORMD_S3_SECRET,
  region: "us-east-1",
  virtualHostedStyle: false,
});

const key = "bun/hello world.txt";
const payload = "hello from Bun.S3";
const written = await client.write(key, payload, { type: "text/plain" });
if (written !== Buffer.byteLength(payload)) throw new Error("unexpected written size: " + written);

const file = client.file(key);
if (!(await file.exists())) throw new Error("written object does not exist");
const stat = await file.stat();
if (stat.size !== Buffer.byteLength(payload) || !stat.type.startsWith("text/plain")) {
  throw new Error("unexpected stat: size=" + stat.size + " type=" + stat.type);
}
if ((await file.text()) !== payload) throw new Error("downloaded object does not match");
if ((await file.slice(6, 10).text()) !== "from") throw new Error("range download does not match");

const listed = await client.list({ prefix: "bun/", maxKeys: 10 });
if (listed.keyCount !== 1 || listed.contents?.[0]?.key !== key) {
  throw new Error("unexpected list response: " + JSON.stringify(listed));
}

const presignedGet = client.presign(key, { method: "GET", expiresIn: 60 });
const getResponse = await fetch(presignedGet);
if (!getResponse.ok || (await getResponse.text()) !== payload) throw new Error("presigned GET failed");

const presignedKey = "bun/presigned.txt";
const presignedPut = client.presign(presignedKey, { method: "PUT", expiresIn: 60 });
const putResponse = await fetch(presignedPut, { method: "PUT", body: "presigned by Bun.S3" });
if (!putResponse.ok) throw new Error("presigned PUT failed: " + putResponse.status);
if ((await client.file(presignedKey).text()) !== "presigned by Bun.S3") throw new Error("presigned object does not match");

const multipartKey = "bun/multipart.bin";
const partSize = 5 * 1024 * 1024;
const writer = client.file(multipartKey).writer({ partSize, queueSize: 1 });
await writer.write("a".repeat(partSize));
await writer.write("tail");
await writer.end();
const multipartFile = client.file(multipartKey);
if ((await multipartFile.stat()).size !== partSize + 4) throw new Error("multipart object size does not match");
if ((await multipartFile.slice(partSize - 2, partSize + 4).text()) !== "aatail") {
  throw new Error("multipart range does not match");
}

await client.delete(key);
await client.delete(presignedKey);
await client.delete(multipartKey);
if (await file.exists()) throw new Error("deleted object still exists");

console.log("Bun.S3 contract passed");
`
