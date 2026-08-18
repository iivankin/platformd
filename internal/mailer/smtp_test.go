package mailer

import (
	"net/smtp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatMessageStripsHeaderNewlines(t *testing.T) {
	message := formatMessage(SMTPConfig{
		FromAddress: "alerts@example.com",
		FromName:    "platformd\r\nBcc: evil@example.com",
	}, Message{
		To:      []string{"ops@example.com"},
		Subject: "Alert\nInjected: yes",
		Body:    "ok",
	})
	if strings.Contains(message, "\nBcc:") || strings.Contains(message, "\nInjected:") {
		t.Fatalf("header injection survived: %q", message)
	}
	if !strings.Contains(message, "From: platformdBcc: evil@example.com <alerts@example.com>\r\n") {
		t.Fatalf("from header = %q", message)
	}
	if !strings.Contains(message, "Subject: AlertInjected: yes\r\n") {
		t.Fatalf("subject header = %q", message)
	}
	if !strings.Contains(message, "Date: ") {
		t.Fatalf("missing Date header: %q", message)
	}
}

func TestSMTPAuthAllowsPLAINWithoutTLSWhenEncryptionIsNone(t *testing.T) {
	auth := smtpAuth(SMTPConfig{Host: "smtp.example.com", Username: "alerts", Encryption: "none"}, []byte("secret"))
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "smtp.example.com"}); err != nil {
		t.Fatal(err)
	}
	auth = smtpAuth(SMTPConfig{Host: "smtp.example.com", Username: "alerts", Encryption: "starttls"}, []byte("secret"))
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "smtp.example.com"}); err == nil {
		t.Fatal("expected unencrypted AUTH to fail for STARTTLS")
	}
}

func TestFirstLineTruncatesUnicodeWithoutSplittingRunes(t *testing.T) {
	got := firstLine(strings.Repeat("й", 130))
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("split rune: %q", got)
	}
	if utf8.RuneCountInString(strings.TrimSuffix(got, "...")) != 117 {
		t.Fatalf("truncated = %q", got)
	}
}
