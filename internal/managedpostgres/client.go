package managedpostgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	Port                   = 5432
	MaximumSQLBytes        = 256 << 10
	MaximumQueryRows       = 1000
	MaximumQueryBytes      = 1 << 20
	MaximumQueryStatements = 32
	QueryTimeout           = 30 * time.Second
)

var (
	ErrInvalidQuery = errors.New("invalid managed PostgreSQL query")
	ErrNotRunning   = errors.New("managed PostgreSQL resource is not running")
)

type Column struct {
	Name    string `json:"name"`
	TypeOID uint32 `json:"typeOid"`
}

type Cell struct {
	Null   bool   `json:"null,omitempty"`
	Text   string `json:"text,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

type StatementResult struct {
	Columns    []Column `json:"columns"`
	Rows       [][]Cell `json:"rows"`
	CommandTag string   `json:"commandTag"`
	Truncated  bool     `json:"truncated"`
}

type QueryResult struct {
	Statements []StatementResult `json:"statements"`
	Truncated  bool              `json:"truncated"`
}

type Client struct {
	connection *pgx.Conn
}

func Dial(ctx context.Context, address, username, password, database string) (*Client, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse managed PostgreSQL address: %w", err)
	}
	connectionURL := &url.URL{
		Scheme: "postgres", User: url.UserPassword(username, password),
		Host: net.JoinHostPort(host, port), Path: "/" + database,
		RawQuery: "sslmode=disable",
	}
	config, err := pgx.ParseConfig(connectionURL.String())
	if err != nil {
		return nil, err
	}
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return &Client{connection: connection}, nil
}

func (client *Client) Close(ctx context.Context) error {
	return client.connection.Close(ctx)
}

func (client *Client) Ping(ctx context.Context) error {
	var one int
	if err := client.connection.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return errors.New("managed PostgreSQL readiness query returned an unexpected value")
	}
	return nil
}

func (client *Client) Bootstrap(ctx context.Context, database, owner, password string) error {
	if database == "" || owner == "" || !validPassword(password) {
		return errors.New("managed PostgreSQL bootstrap input is invalid")
	}
	ownerIdentifier := pgx.Identifier{owner}.Sanitize()
	databaseIdentifier := pgx.Identifier{database}.Sanitize()
	var roleExists bool
	if err := client.connection.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", owner).Scan(&roleExists); err != nil {
		return err
	}
	if !roleExists {
		if _, err := client.connection.Exec(ctx, "CREATE ROLE "+ownerIdentifier+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION"); err != nil {
			return err
		}
	}
	if _, err := client.connection.Exec(ctx, "ALTER ROLE "+ownerIdentifier+" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD "+quoteLiteral(password)); err != nil {
		return err
	}
	var databaseExists bool
	if err := client.connection.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", database).Scan(&databaseExists); err != nil {
		return err
	}
	if !databaseExists {
		if _, err := client.connection.Exec(ctx, "CREATE DATABASE "+databaseIdentifier+" OWNER "+ownerIdentifier); err != nil {
			return err
		}
	}
	_, err := client.connection.Exec(ctx, "ALTER DATABASE "+databaseIdentifier+" OWNER TO "+ownerIdentifier)
	return err
}

func (client *Client) Query(ctx context.Context, sql string) (QueryResult, error) {
	if strings.TrimSpace(sql) == "" || len(sql) > MaximumSQLBytes {
		return QueryResult{}, fmt.Errorf("%w: SQL must be 1..256 KiB", ErrInvalidQuery)
	}
	reader := client.connection.PgConn().Exec(ctx, sql)
	result := QueryResult{Statements: make([]StatementResult, 0, 1)}
	remainingRows := MaximumQueryRows
	remainingBytes := MaximumQueryBytes
	for reader.NextResult() {
		if len(result.Statements) == MaximumQueryStatements {
			result.Truncated = true
			break
		}
		statement, err := readStatement(reader.ResultReader(), &remainingRows, &remainingBytes)
		if err != nil {
			_ = reader.Close()
			return QueryResult{}, err
		}
		result.Truncated = result.Truncated || statement.Truncated
		result.Statements = append(result.Statements, statement)
	}
	if err := reader.Close(); err != nil {
		return QueryResult{}, err
	}
	return result, nil
}

func readStatement(reader *pgconn.ResultReader, remainingRows, remainingBytes *int) (StatementResult, error) {
	fields := reader.FieldDescriptions()
	statement := StatementResult{
		Columns: make([]Column, len(fields)), Rows: make([][]Cell, 0),
	}
	for index, field := range fields {
		statement.Columns[index] = Column{Name: field.Name, TypeOID: field.DataTypeOID}
	}
	for reader.NextRow() {
		values := reader.Values()
		rowBytes := 0
		for _, value := range values {
			rowBytes += len(value)
		}
		if *remainingRows == 0 || rowBytes > *remainingBytes {
			statement.Truncated = true
			break
		}
		row := make([]Cell, len(values))
		for index, value := range values {
			row[index] = encodeCell(value)
		}
		statement.Rows = append(statement.Rows, row)
		*remainingRows--
		*remainingBytes -= rowBytes
	}
	commandTag, err := reader.Close()
	statement.CommandTag = commandTag.String()
	return statement, err
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func encodeCell(value []byte) Cell {
	if value == nil {
		return Cell{Null: true}
	}
	if utf8.Valid(value) {
		return Cell{Text: string(value)}
	}
	return Cell{Base64: base64.RawURLEncoding.EncodeToString(value)}
}

func QueryErrorClass(err error) string {
	if err == nil {
		return ""
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "runtime"
}
