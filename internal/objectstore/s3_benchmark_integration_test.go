//go:build integration

package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3BenchmarkBackend struct {
	name   string
	client *minio.Client
	bucket string
}

type s3BenchmarkWorkload struct {
	name    string
	size    int64
	payload []byte
}

func BenchmarkS3Backends(benchmark *testing.B) {
	if os.Getenv("PLATFORMD_S3_BENCHMARK") != "1" {
		benchmark.Skip("set PLATFORMD_S3_BENCHMARK=1 and PLATFORMD_MINIO_* to compare platformd with MinIO")
	}
	platformd := startSDKContractServer(benchmark)
	platformdClient := benchmarkS3Client(benchmark, platformd.endpoint, platformd.accessKey, platformd.secret)
	minioBackend := externalMinIOBenchmarkBackend(benchmark)
	backends := []s3BenchmarkBackend{
		{name: "platformd", client: platformdClient, bucket: platformd.bucket},
		platformdTLSHopBenchmarkBackend(benchmark, platformd),
		minioBackend,
	}

	workloads := []s3BenchmarkWorkload{
		{name: "4KiB", size: 4 << 10, payload: bytes.Repeat([]byte{0x5a}, 4<<10)},
		{name: "1MiB", size: 1 << 20, payload: bytes.Repeat([]byte{0x5a}, 1<<20)},
		{name: "8MiB", size: 8 << 20, payload: bytes.Repeat([]byte{0x5a}, 8<<20)},
	}
	if os.Getenv("PLATFORMD_S3_BENCHMARK_LARGE") == "1" {
		// Generate large bodies per operation so the benchmark measures streaming
		// memory rather than retaining a 1 GiB client-side byte slice.
		workloads = append(workloads, s3BenchmarkWorkload{name: "1GiB", size: 1 << 30})
	}
	for _, workload := range workloads {
		for _, backend := range backends {
			backend := backend
			benchmark.Run("put/"+workload.name+"/"+backend.name, func(benchmark *testing.B) {
				benchmarkPutObject(benchmark, backend, "benchmark/put-"+workload.name, workload, true)
			})
			benchmark.Run("get/"+workload.name+"/"+backend.name, func(benchmark *testing.B) {
				benchmarkGetObject(benchmark, backend, "benchmark/get-"+workload.name, workload)
			})
		}
	}

	multipartWorkloads := []s3BenchmarkWorkload{{
		name: "20MiB", size: 20 << 20, payload: bytes.Repeat([]byte{0xa5}, 20<<20),
	}}
	if os.Getenv("PLATFORMD_S3_BENCHMARK_LARGE") == "1" {
		multipartWorkloads = append(multipartWorkloads, s3BenchmarkWorkload{name: "1GiB", size: 1 << 30})
	}
	for _, workload := range multipartWorkloads {
		for _, backend := range backends {
			backend := backend
			benchmark.Run("multipart-put/"+workload.name+"/"+backend.name, func(benchmark *testing.B) {
				benchmarkPutObject(benchmark, backend, "benchmark/multipart-"+workload.name, workload, false)
			})
		}
	}

	if os.Getenv("PLATFORMD_S3_BENCHMARK_MATRIX") == "1" {
		benchmarkConcurrencyMatrix(benchmark, backends)
	}
}

func benchmarkConcurrencyMatrix(benchmark *testing.B, backends []s3BenchmarkBackend) {
	workloads := []s3BenchmarkWorkload{
		{name: "4KiB", size: 4 << 10, payload: bytes.Repeat([]byte{0x3c}, 4<<10)},
		{name: "1MiB", size: 1 << 20, payload: bytes.Repeat([]byte{0x3c}, 1<<20)},
	}
	rotatingKeys := benchmarkRotatingKeyCount(benchmark)
	for _, workload := range workloads {
		for _, backend := range backends {
			backend := backend
			for _, concurrency := range []int{1, 8, 32, 128} {
				concurrency := concurrency
				benchmark.Run(fmt.Sprintf("matrix/put/%s/%s/c%d", workload.name, backend.name, concurrency), func(benchmark *testing.B) {
					benchmarkConcurrentPut(benchmark, backend, workload, concurrency)
				})
				benchmark.Run(fmt.Sprintf("matrix/warm-get/%s/%s/c%d", workload.name, backend.name, concurrency), func(benchmark *testing.B) {
					benchmarkConcurrentGet(benchmark, backend, workload, concurrency, 1)
				})
				if rotatingKeys > 1 {
					benchmark.Run(fmt.Sprintf("matrix/rotating-get/%s/%s/c%d", workload.name, backend.name, concurrency), func(benchmark *testing.B) {
						benchmarkConcurrentGet(benchmark, backend, workload, concurrency, rotatingKeys)
					})
				}
			}
		}
	}
}

func benchmarkConcurrentPut(
	benchmark *testing.B,
	backend s3BenchmarkBackend,
	workload s3BenchmarkWorkload,
	concurrency int,
) {
	benchmark.Helper()
	keys := make([]string, concurrency)
	for worker := range concurrency {
		keys[worker] = fmt.Sprintf("benchmark/matrix-put-%s-c%d-w%d", workload.name, concurrency, worker)
	}
	benchmark.Cleanup(func() { removeBenchmarkKeys(backend, keys) })
	benchmark.SetBytes(workload.size)
	benchmark.ReportAllocs()
	runConcurrentBenchmark(benchmark, concurrency, func(worker, _ int) error {
		_, err := backend.client.PutObject(
			context.Background(), backend.bucket, keys[worker], workload.reader(), workload.size,
			minio.PutObjectOptions{ContentEncoding: "aws-chunked", DisableMultipart: true},
		)
		return err
	})
}

func benchmarkConcurrentGet(
	benchmark *testing.B,
	backend s3BenchmarkBackend,
	workload s3BenchmarkWorkload,
	concurrency int,
	keyCount int,
) {
	benchmark.Helper()
	keys := make([]string, keyCount)
	for index := range keyCount {
		keys[index] = fmt.Sprintf(
			"benchmark/matrix-get-%s-keys%d-%d", workload.name, keyCount, index,
		)
		if _, err := backend.client.PutObject(
			context.Background(), backend.bucket, keys[index], workload.reader(), workload.size,
			minio.PutObjectOptions{ContentEncoding: "aws-chunked", DisableMultipart: true},
		); err != nil {
			benchmark.Fatal(err)
		}
	}
	benchmark.Cleanup(func() { removeBenchmarkKeys(backend, keys) })
	benchmark.SetBytes(workload.size)
	benchmark.ReportAllocs()
	runConcurrentBenchmark(benchmark, concurrency, func(worker, iteration int) error {
		key := keys[(worker+iteration*concurrency)%len(keys)]
		object, err := backend.client.GetObject(context.Background(), backend.bucket, key, minio.GetObjectOptions{})
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(io.Discard, object)
		closeErr := object.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != workload.size {
			return fmt.Errorf("GET copied %d bytes, expected %d", written, workload.size)
		}
		return nil
	})
}

func runConcurrentBenchmark(
	benchmark *testing.B,
	concurrency int,
	operation func(worker, iteration int) error,
) {
	benchmark.Helper()
	workers := min(concurrency, benchmark.N)
	benchmark.ReportMetric(float64(workers), "workers")
	durations := make([]int64, benchmark.N)
	start := make(chan struct{})
	var group sync.WaitGroup
	var failureOnce sync.Once
	var failure error
	group.Add(workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			<-start
			iteration := 0
			for index := worker; index < benchmark.N; index += workers {
				began := time.Now()
				err := operation(worker, iteration)
				durations[index] = time.Since(began).Nanoseconds()
				if err != nil {
					failureOnce.Do(func() { failure = err })
					return
				}
				iteration++
			}
		}()
	}
	benchmark.ResetTimer()
	close(start)
	group.Wait()
	benchmark.StopTimer()
	if failure != nil {
		benchmark.Fatal(failure)
	}
	reportLatencyPercentiles(benchmark, durations)
}

func reportLatencyPercentiles(benchmark *testing.B, durations []int64) {
	benchmark.Helper()
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	for _, percentile := range []int{50, 95, 99} {
		index := (len(durations)*percentile + 99) / 100
		index = max(index, 1) - 1
		benchmark.ReportMetric(float64(durations[index]), fmt.Sprintf("p%d-ns/op", percentile))
	}
}

func benchmarkRotatingKeyCount(benchmark *testing.B) int {
	benchmark.Helper()
	value := os.Getenv("PLATFORMD_S3_BENCHMARK_ROTATING_KEYS")
	if value == "" {
		return 0
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 2 || count > 10_000 {
		benchmark.Fatalf("PLATFORMD_S3_BENCHMARK_ROTATING_KEYS must be 2..10000, got %q", value)
	}
	return count
}

func removeBenchmarkKeys(backend s3BenchmarkBackend, keys []string) {
	for _, key := range keys {
		_ = backend.client.RemoveObject(context.Background(), backend.bucket, key, minio.RemoveObjectOptions{})
	}
}

func benchmarkPutObject(
	benchmark *testing.B,
	backend s3BenchmarkBackend,
	key string,
	workload s3BenchmarkWorkload,
	singlePart bool,
) {
	benchmark.Helper()
	options := minio.PutObjectOptions{DisableMultipart: singlePart, PartSize: 5 << 20}
	if singlePart {
		options.ContentEncoding = "aws-chunked"
	}
	benchmark.SetBytes(workload.size)
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		if _, err := backend.client.PutObject(
			context.Background(), backend.bucket, key, workload.reader(), workload.size, options,
		); err != nil {
			benchmark.Fatal(err)
		}
	}
	benchmark.StopTimer()
	benchmark.Cleanup(func() {
		_ = backend.client.RemoveObject(context.Background(), backend.bucket, key, minio.RemoveObjectOptions{})
	})
}

func benchmarkGetObject(benchmark *testing.B, backend s3BenchmarkBackend, key string, workload s3BenchmarkWorkload) {
	benchmark.Helper()
	if _, err := backend.client.PutObject(
		context.Background(), backend.bucket, key, workload.reader(), workload.size,
		minio.PutObjectOptions{ContentEncoding: "aws-chunked", DisableMultipart: true},
	); err != nil {
		benchmark.Fatal(err)
	}
	benchmark.Cleanup(func() {
		_ = backend.client.RemoveObject(context.Background(), backend.bucket, key, minio.RemoveObjectOptions{})
	})
	benchmark.SetBytes(workload.size)
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		object, err := backend.client.GetObject(context.Background(), backend.bucket, key, minio.GetObjectOptions{})
		if err != nil {
			benchmark.Fatal(err)
		}
		written, copyErr := io.Copy(io.Discard, object)
		closeErr := object.Close()
		if copyErr != nil || closeErr != nil || written != workload.size {
			benchmark.Fatalf("GET copied %d bytes: copy=%v close=%v", written, copyErr, closeErr)
		}
	}
}

func (workload s3BenchmarkWorkload) reader() io.Reader {
	if workload.payload != nil {
		return bytes.NewReader(workload.payload)
	}
	return &generatedBenchmarkReader{remaining: workload.size}
}

type generatedBenchmarkReader struct {
	remaining int64
}

func (reader *generatedBenchmarkReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	length := min(int64(len(buffer)), reader.remaining)
	clear(buffer[:length])
	reader.remaining -= length
	return int(length), nil
}

func externalMinIOBenchmarkBackend(benchmark *testing.B) s3BenchmarkBackend {
	benchmark.Helper()
	endpoint := os.Getenv("PLATFORMD_MINIO_ENDPOINT")
	accessKey := os.Getenv("PLATFORMD_MINIO_ACCESS_KEY")
	secret := os.Getenv("PLATFORMD_MINIO_SECRET")
	if endpoint == "" || accessKey == "" || secret == "" {
		benchmark.Skip("PLATFORMD_MINIO_ENDPOINT, PLATFORMD_MINIO_ACCESS_KEY, and PLATFORMD_MINIO_SECRET are required")
	}
	bucket := os.Getenv("PLATFORMD_MINIO_BUCKET")
	if bucket == "" {
		bucket = "platformd-s3-benchmark"
	}
	client := benchmarkS3Client(benchmark, endpoint, accessKey, secret)
	exists, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		benchmark.Fatal(err)
	}
	created := false
	if !exists {
		if err := client.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{Region: Region}); err != nil {
			benchmark.Fatal(err)
		}
		created = true
	}
	if created {
		benchmark.Cleanup(func() { _ = client.RemoveBucket(context.Background(), bucket) })
	}
	return s3BenchmarkBackend{name: "minio", client: client, bucket: bucket}
}

func platformdTLSHopBenchmarkBackend(
	benchmark *testing.B,
	fixture sdkContractFixture,
) s3BenchmarkBackend {
	benchmark.Helper()
	target, err := url.Parse(fixture.endpoint)
	if err != nil {
		benchmark.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = target.Scheme
			request.Out.URL.Host = target.Host
			// SigV4 covers Host. The public proxy changes only the transport
			// destination. This benchmark isolates that TLS/reverse-proxy hop and
			// intentionally does not include production's SQLite hostname lookup.
			request.Out.Host = request.In.Host
			request.Out.Header.Set("X-Platformd-Public-Store", fixture.storeID)
		},
		Transport: &http.Transport{
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 256,
			ReadBufferSize:      64 << 10,
			WriteBufferSize:     64 << 10,
		},
		BufferPool: benchmarkProxyBuffers{pool: &sync.Pool{New: func() any {
			buffer := make([]byte, 128<<10)
			return &buffer
		}}},
	}
	server := httptest.NewUnstartedServer(proxy)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	benchmark.Cleanup(server.Close)
	return s3BenchmarkBackend{
		name: "platformd-tls-hop",
		client: benchmarkS3ClientWithTransport(
			benchmark,
			server.URL,
			fixture.accessKey,
			fixture.secret,
			server.Client().Transport,
		),
		bucket: fixture.bucket,
	}
}

type benchmarkProxyBuffers struct {
	pool *sync.Pool
}

func (buffers benchmarkProxyBuffers) Get() []byte {
	return *buffers.pool.Get().(*[]byte)
}

func (buffers benchmarkProxyBuffers) Put(buffer []byte) {
	if cap(buffer) != 128<<10 {
		return
	}
	buffer = buffer[:cap(buffer)]
	buffers.pool.Put(&buffer)
}

func benchmarkS3Client(benchmark *testing.B, endpoint, accessKey, secret string) *minio.Client {
	return benchmarkS3ClientWithTransport(benchmark, endpoint, accessKey, secret, nil)
}

func benchmarkS3ClientWithTransport(
	benchmark *testing.B,
	endpoint string,
	accessKey string,
	secret string,
	transport http.RoundTripper,
) *minio.Client {
	benchmark.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") {
		benchmark.Fatalf("invalid S3 benchmark endpoint %q", endpoint)
	}
	client, err := minio.New(parsed.Host, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secret, ""), Secure: parsed.Scheme == "https", Region: Region,
		Transport: transport,
	})
	if err != nil {
		benchmark.Fatal(err)
	}
	return client
}
