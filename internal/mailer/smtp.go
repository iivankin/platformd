package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	FromAddress string
	FromName    string
	Encryption  string
}

type Message struct {
	To      []string
	Subject string
	Body    string
}

func sendSMTP(ctx context.Context, config SMTPConfig, password []byte, message Message) error {
	if config.Host == "" || config.Port < 1 || config.Port > 65535 || config.FromAddress == "" ||
		len(message.To) == 0 || message.Subject == "" {
		return errors.New("SMTP message is incomplete")
	}
	addr := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Deadline = deadline
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: tlsServerName(config.Host)}
	var conn net.Conn
	var err error
	if config.Encryption == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open SMTP session: %w", err)
	}
	defer func() { _ = client.Close() }()
	if config.Encryption == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if auth := smtpAuth(config, password); auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication: %w", err)
		}
	}
	if err := client.Mail(config.FromAddress); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, recipient := range message.To {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := io.WriteString(writer, formatMessage(config, message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close SMTP message: %w", err)
	}
	return client.Quit()
}

func formatMessage(config SMTPConfig, message Message) string {
	from := config.FromAddress
	if config.FromName != "" {
		from = mime.QEncoding.Encode("utf-8", sanitizeHeader(config.FromName)) + " <" + config.FromAddress + ">"
	}
	var builder strings.Builder
	builder.WriteString("From: " + from + "\r\n")
	builder.WriteString("To: " + strings.Join(message.To, ", ") + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", sanitizeHeader(message.Subject)) + "\r\n")
	builder.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(strings.ReplaceAll(message.Body, "\n", "\r\n"))
	if !strings.HasSuffix(message.Body, "\n") {
		builder.WriteString("\r\n")
	}
	return builder.String()
}

type plainAuth struct {
	username, password, host string
	allowUnencrypted         bool
}

func smtpAuth(config SMTPConfig, password []byte) smtp.Auth {
	if config.Username == "" {
		return nil
	}
	return &plainAuth{
		username:         config.Username,
		password:         string(password),
		host:             config.Host,
		allowUnencrypted: config.Encryption == "none",
	}
}

func (auth *plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if server == nil {
		return "", nil, errors.New("SMTP server info is missing")
	}
	if !server.TLS && !auth.allowUnencrypted && !isLocalSMTPHost(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != auth.host {
		return "", nil, errors.New("wrong host name")
	}
	return "PLAIN", []byte("\x00" + auth.username + "\x00" + auth.password), nil
}

func (auth *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

func isLocalSMTPHost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

func sanitizeHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
}

func tlsServerName(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ""
	}
	return host
}
