package managedredis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultOperationTimeout = 2 * time.Second
	maximumCommandBytes     = 1 << 20
	maximumLineBytes        = 64 << 10
	maximumBulkBytes        = 1 << 20
	maximumArrayElements    = 4096
	maximumResponseBytes    = 2 << 20
	maximumNestingDepth     = 8
)

type responseKind byte

const (
	responseString responseKind = iota
	responseInteger
	responseBulk
	responseArray
	responseNull
)

type response struct {
	kind    responseKind
	text    string
	integer int64
	bulk    []byte
	array   []response
}

type Client struct {
	connection net.Conn
	reader     *bufio.Reader
	mu         sync.Mutex
}

func Dial(ctx context.Context, address, password string) (*Client, error) {
	if address == "" || password == "" {
		return nil, errors.New("Redis address and password are required")
	}
	connection, err := (&net.Dialer{Timeout: defaultOperationTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial Redis: %w", err)
	}
	client := &Client{connection: connection, reader: bufio.NewReaderSize(connection, 32<<10)}
	if err := client.authenticate(ctx, password); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return client, nil
}

func (client *Client) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	return client.connection.Close()
}

func (client *Client) Ping(ctx context.Context) error {
	return client.expectOK(ctx, "PONG", "PING")
}

func (client *Client) Save(ctx context.Context) error {
	return client.expectOK(ctx, "OK", "SAVE")
}

func (client *Client) expectOK(ctx context.Context, expected string, command ...string) error {
	value, err := client.command(ctx, command...)
	if err != nil {
		return err
	}
	if value.kind != responseString || value.text != expected {
		return fmt.Errorf("unexpected Redis %s response", command[0])
	}
	return nil
}

func (client *Client) authenticate(ctx context.Context, password string) error {
	value, err := client.command(ctx, "AUTH", password)
	if err != nil {
		return fmt.Errorf("authenticate Redis: %w", err)
	}
	if value.kind != responseString || value.text != "OK" {
		return errors.New("unexpected Redis AUTH response")
	}
	return nil
}

func (client *Client) command(ctx context.Context, arguments ...string) (response, error) {
	if err := ctx.Err(); err != nil {
		return response{}, err
	}
	if client == nil || client.connection == nil || len(arguments) == 0 || len(arguments) > maximumArrayElements {
		return response{}, errors.New("invalid Redis command")
	}
	encodedSize := len(strconv.Itoa(len(arguments))) + 3
	for _, argument := range arguments {
		encodedSize += len(strconv.Itoa(len(argument))) + len(argument) + 5
		if encodedSize > maximumCommandBytes {
			return response{}, errors.New("Redis command exceeds 1 MiB")
		}
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return response{}, err
	}
	deadline := time.Now().Add(defaultOperationTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := client.connection.SetDeadline(deadline); err != nil {
		return response{}, fmt.Errorf("set Redis operation deadline: %w", err)
	}
	if err := writeCommand(client.connection, arguments); err != nil {
		return response{}, fmt.Errorf("write Redis command: %w", err)
	}
	budget := maximumResponseBytes
	value, err := readResponse(client.reader, 0, &budget)
	if err != nil {
		return response{}, fmt.Errorf("read Redis response: %w", err)
	}
	return value, nil
}

func writeCommand(writer io.Writer, arguments []string) error {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(arguments)); err != nil {
		return err
	}
	for _, argument := range arguments {
		if _, err := fmt.Fprintf(writer, "$%d\r\n", len(argument)); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, argument); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, "\r\n"); err != nil {
			return err
		}
	}
	return nil
}

func readResponse(reader *bufio.Reader, depth int, budget *int) (response, error) {
	if depth > maximumNestingDepth {
		return response{}, errors.New("Redis response nesting is too deep")
	}
	prefix, err := reader.ReadByte()
	if err != nil {
		return response{}, err
	}
	if err := consumeBudget(budget, 1); err != nil {
		return response{}, err
	}
	switch prefix {
	case '+', '-', ':', '$', '*':
	default:
		return response{}, fmt.Errorf("unsupported Redis response prefix %q", prefix)
	}
	line, err := readLine(reader, budget)
	if err != nil {
		return response{}, err
	}
	switch prefix {
	case '+':
		return response{kind: responseString, text: string(line)}, nil
	case '-':
		return response{}, fmt.Errorf("Redis error: %s", sanitizeError(line))
	case ':':
		value, err := parseDecimal(line)
		return response{kind: responseInteger, integer: value}, err
	case '$':
		length, err := parseLength(line, maximumBulkBytes)
		if err != nil {
			return response{}, err
		}
		if length == -1 {
			return response{kind: responseNull}, nil
		}
		if err := consumeBudget(budget, length+2); err != nil {
			return response{}, err
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return response{}, err
		}
		if value[length] != '\r' || value[length+1] != '\n' {
			return response{}, errors.New("Redis bulk response has invalid terminator")
		}
		return response{kind: responseBulk, bulk: value[:length]}, nil
	case '*':
		length, err := parseLength(line, maximumArrayElements)
		if err != nil {
			return response{}, err
		}
		if length == -1 {
			return response{kind: responseNull}, nil
		}
		values := make([]response, 0, length)
		for index := 0; index < length; index++ {
			value, err := readResponse(reader, depth+1, budget)
			if err != nil {
				return response{}, err
			}
			values = append(values, value)
		}
		return response{kind: responseArray, array: values}, nil
	default:
		panic("validated Redis response prefix")
	}
}

func readLine(reader *bufio.Reader, budget *int) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maximumLineBytes {
		return nil, errors.New("Redis response line exceeds 64 KiB")
	}
	if err != nil {
		return nil, err
	}
	if err := consumeBudget(budget, len(line)); err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errors.New("Redis response line has invalid terminator")
	}
	return line[:len(line)-2], nil
}

func parseLength(value []byte, maximum int) (int, error) {
	parsed, err := strconv.ParseInt(string(value), 10, 32)
	if err != nil || parsed < -1 || parsed > int64(maximum) {
		return 0, errors.New("Redis response length is invalid or exceeds its bound")
	}
	return int(parsed), nil
}

func parseDecimal(value []byte) (int64, error) {
	parsed, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0, errors.New("Redis integer response is invalid")
	}
	return parsed, nil
}

func consumeBudget(budget *int, amount int) error {
	if amount < 0 || *budget < amount {
		return errors.New("Redis response exceeds 2 MiB")
	}
	*budget -= amount
	return nil
}

func sanitizeError(value []byte) string {
	const maximumErrorBytes = 512
	if len(value) > maximumErrorBytes {
		value = value[:maximumErrorBytes]
	}
	return strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return '�'
		}
		return character
	}, string(value))
}
