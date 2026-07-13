package managedredis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestDialAuthenticatesAndPingsUsingBoundedRESP(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		for _, expected := range [][]string{{"AUTH", "secret"}, {"PING"}} {
			actual, readErr := readTestCommand(reader)
			if readErr != nil {
				serverDone <- readErr
				return
			}
			if fmt.Sprint(actual) != fmt.Sprint(expected) {
				serverDone <- fmt.Errorf("command = %v, want %v", actual, expected)
				return
			}
			response := "+OK\r\n"
			if expected[0] == "PING" {
				response = "+PONG\r\n"
			}
			if _, writeErr := connection.Write([]byte(response)); writeErr != nil {
				serverDone <- writeErr
				return
			}
		}
		serverDone <- nil
	}()
	client, err := Dial(context.Background(), listener.Addr().String(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestRESPRejectsOversizedAndMalformedResponses(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"bulk":   fmt.Sprintf("$%d\r\n", maximumBulkBytes+1),
		"array":  fmt.Sprintf("*%d\r\n", maximumArrayElements+1),
		"line":   "+" + strings.Repeat("a", maximumLineBytes) + "\r\n",
		"prefix": "?wat\r\n",
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			budget := maximumResponseBytes
			if _, err := readResponse(bufio.NewReader(strings.NewReader(payload)), 0, &budget); err == nil {
				t.Fatal("malformed response was accepted")
			}
		})
	}
}

func TestParsePersistenceStatusRequiresExactRDBFields(t *testing.T) {
	t.Parallel()
	status, err := parsePersistenceStatus([]byte("# Persistence\r\nrdb_bgsave_in_progress:1\r\nrdb_last_bgsave_status:ok\r\n"))
	if err != nil || !status.BackgroundSaveInProgress || !status.LastBackgroundSaveOK {
		t.Fatalf("persistence status = %+v, %v", status, err)
	}
	status, err = parsePersistenceStatus([]byte("rdb_bgsave_in_progress:0\r\nrdb_last_bgsave_status:err\r\n"))
	if err != nil || status.BackgroundSaveInProgress || status.LastBackgroundSaveOK {
		t.Fatalf("failed persistence status = %+v, %v", status, err)
	}
	if _, err := parsePersistenceStatus([]byte("rdb_bgsave_in_progress:0\r\n")); err == nil {
		t.Fatal("missing last background-save status was accepted")
	}
}

func readTestCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	var count int
	if _, err := fmt.Sscanf(line, "*%d\r\n", &count); err != nil {
		return nil, err
	}
	result := make([]string, 0, count)
	for index := 0; index < count; index++ {
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		var length int
		if _, err := fmt.Sscanf(line, "$%d\r\n", &length); err != nil {
			return nil, err
		}
		value := make([]byte, length+2)
		if _, err := ioReadFull(reader, value); err != nil {
			return nil, err
		}
		if string(value[length:]) != "\r\n" {
			return nil, errors.New("invalid command terminator")
		}
		result = append(result, string(value[:length]))
	}
	return result, nil
}

func ioReadFull(reader *bufio.Reader, value []byte) (int, error) {
	read := 0
	for read < len(value) {
		count, err := reader.Read(value[read:])
		read += count
		if err != nil {
			return read, err
		}
	}
	return read, nil
}
