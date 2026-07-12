package sdnotify

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

func Ready(status string) error {
	message := "READY=1"
	if status != "" {
		message += "\nSTATUS=" + sanitize(status)
	}
	return Notify(message)
}

func Stopping(status string) error {
	message := "STOPPING=1"
	if status != "" {
		message += "\nSTATUS=" + sanitize(status)
	}
	return Notify(message)
}

func Notify(message string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	if strings.ContainsRune(message, '\x00') || message == "" {
		return errors.New("sd_notify message is invalid")
	}
	if strings.HasPrefix(path, "@") {
		path = "\x00" + path[1:]
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("connect sd_notify socket: %w", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte(message)); err != nil {
		return fmt.Errorf("send sd_notify message: %w", err)
	}
	return nil
}

func sanitize(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return value
}
