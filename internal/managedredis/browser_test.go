package managedredis

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"testing"
)

func TestScanKeysUsesSCANAndBoundedReadOnlyMetadataCommands(t *testing.T) {
	t.Parallel()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := &Client{connection: clientSide, reader: bufio.NewReader(clientSide)}
	done := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(serverSide)
		exchanges := []struct {
			command  []string
			response string
		}{
			{[]string{"SCAN", "0", "MATCH", "user:*", "COUNT", "2"}, "*2\r\n$1\r\n7\r\n*2\r\n$6\r\nuser:1\r\n$6\r\nuser:2\r\n"},
			{[]string{"TYPE", "user:1"}, "+string\r\n"},
			{[]string{"PTTL", "user:1"}, ":1500\r\n"},
			{[]string{"MEMORY", "USAGE", "user:1"}, ":96\r\n"},
			{[]string{"TYPE", "user:2"}, "+none\r\n"},
		}
		for _, exchange := range exchanges {
			command, err := readTestCommand(reader)
			if err != nil {
				done <- err
				return
			}
			if fmt.Sprint(command) != fmt.Sprint(exchange.command) {
				done <- fmt.Errorf("command = %v, want %v", command, exchange.command)
				return
			}
			if _, err := serverSide.Write([]byte(exchange.response)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	page, err := client.ScanKeys(context.Background(), ScanQuery{Match: "user:*", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != 7 || len(page.Keys) != 1 || string(page.Keys[0].Key) != "user:1" || page.Keys[0].Type != "string" || page.Keys[0].SizeBytes != 96 || page.Keys[0].ExpiresInMillis == nil || *page.Keys[0].ExpiresInMillis != 1500 {
		t.Fatalf("unexpected key page: %+v", page)
	}
}

func TestScanKeysRejectsUnboundedCountBeforeNetwork(t *testing.T) {
	t.Parallel()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := &Client{connection: clientSide, reader: bufio.NewReader(clientSide)}
	if _, err := client.ScanKeys(context.Background(), ScanQuery{Count: MaximumScanCount + 1}); err == nil {
		t.Fatal("unbounded SCAN count was accepted")
	}
}

func TestPreviewKeyBoundsStringWithGETRANGE(t *testing.T) {
	t.Parallel()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := &Client{connection: clientSide, reader: bufio.NewReader(clientSide)}
	done := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(serverSide)
		exchanges := []struct {
			command  []string
			response string
		}{
			{[]string{"TYPE", "large"}, "+string\r\n"},
			{[]string{"STRLEN", "large"}, ":70000\r\n"},
			{[]string{"GETRANGE", "large", "0", "65535"}, "$5\r\nhello\r\n"},
		}
		for _, exchange := range exchanges {
			command, err := readTestCommand(reader)
			if err != nil {
				done <- err
				return
			}
			if fmt.Sprint(command) != fmt.Sprint(exchange.command) {
				done <- fmt.Errorf("command = %v, want %v", command, exchange.command)
				return
			}
			if _, err := serverSide.Write([]byte(exchange.response)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	preview, err := client.PreviewKey(context.Background(), PreviewQuery{Key: []byte("large")})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if preview.Type != "string" || preview.Length != 70000 || !preview.Truncated || len(preview.Items) != 1 || string(preview.Items[0].Values[0]) != "hello" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
}
