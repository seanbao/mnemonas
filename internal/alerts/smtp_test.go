package alerts

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestSendSMTPMailWithDialerAppliesDefaultTimeout(t *testing.T) {
	dialStopped := errors.New("dial stopped")
	var dialDeadline time.Time
	before := time.Now()
	err := sendSMTPMailWithDialer(
		context.Background(),
		"smtp.example.com:587",
		nil,
		"alerts@example.com",
		[]string{"admin@example.com"},
		[]byte("test message"),
		defaultSMTPTimeout,
		func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" {
				t.Fatalf("dial network = %q, want tcp", network)
			}
			if address != "smtp.example.com:587" {
				t.Fatalf("dial address = %q, want smtp.example.com:587", address)
			}
			var ok bool
			dialDeadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("SMTP dial context has no deadline")
			}
			return nil, dialStopped
		},
	)
	after := time.Now()
	if !errors.Is(err, dialStopped) {
		t.Fatalf("sendSMTPMailWithDialer() error = %v, want %v", err, dialStopped)
	}
	if dialDeadline.Before(before.Add(defaultSMTPTimeout)) || dialDeadline.After(after.Add(defaultSMTPTimeout)) {
		t.Fatalf("SMTP dial deadline = %v, want a %v default bound", dialDeadline, defaultSMTPTimeout)
	}
}

func TestSendSMTPMailWithDialerBoundsProtocolIOWithoutCallerDeadline(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	const timeout = 50 * time.Millisecond
	started := time.Now()
	err := sendSMTPMailWithDialer(
		context.Background(),
		"smtp.example.com:587",
		nil,
		"alerts@example.com",
		[]string{"admin@example.com"},
		[]byte("test message"),
		timeout,
		func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sendSMTPMailWithDialer() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SMTP protocol read returned after %v, want within 1s", elapsed)
	}
}

func TestSendSMTPMailWithDialerCancellationInterruptsProtocolIO(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	dialed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- sendSMTPMailWithDialer(
			ctx,
			"smtp.example.com:587",
			nil,
			"alerts@example.com",
			[]string{"admin@example.com"},
			[]byte("test message"),
			time.Minute,
			func(context.Context, string, string) (net.Conn, error) {
				close(dialed)
				return clientConn, nil
			},
		)
	}()

	select {
	case <-dialed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SMTP connection")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sendSMTPMailWithDialer() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP protocol read did not stop after context cancellation")
	}
}

func TestSendSMTPMailWithDialerUsesAdvertisedSTARTTLS(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverResult := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		if err := writeSMTPResponse(writer, "220 smtp.example.com ESMTP ready\r\n"); err != nil {
			serverResult <- err
			return
		}
		line, err := readSMTPLine(reader)
		if err != nil {
			serverResult <- err
			return
		}
		if line != "EHLO localhost" {
			serverResult <- fmt.Errorf("first command = %q, want EHLO localhost", line)
			return
		}
		if err := writeSMTPResponse(writer, "250-smtp.example.com\r\n250 STARTTLS\r\n"); err != nil {
			serverResult <- err
			return
		}
		line, err = readSMTPLine(reader)
		if err != nil {
			serverResult <- err
			return
		}
		if line != "STARTTLS" {
			serverResult <- fmt.Errorf("command = %q, want STARTTLS", line)
			return
		}
		serverResult <- writeSMTPResponse(writer, "454 TLS unavailable\r\n")
	}()

	err := sendSMTPMailWithDialer(
		context.Background(),
		"smtp.example.com:587",
		nil,
		"alerts@example.com",
		[]string{"admin@example.com"},
		[]byte("test message"),
		time.Second,
		func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	)
	if err == nil {
		t.Fatal("sendSMTPMailWithDialer() unexpectedly succeeded when STARTTLS was rejected")
	}
	select {
	case serverErr := <-serverResult:
		if serverErr != nil {
			t.Fatalf("SMTP server error: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SMTP server")
	}
}

func TestSendSMTPMailWithDialerPreservesAuthAndDeliveryFlow(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverResult := make(chan error, 1)
	deliveredMessage := make(chan string, 1)
	go func() {
		defer serverConn.Close()
		serverResult <- serveAuthenticatedSMTPTransaction(serverConn, deliveredMessage)
	}()

	err := sendSMTPMailWithDialer(
		context.Background(),
		"localhost:2525",
		smtp.PlainAuth("", "alerts", "secret", "localhost"),
		"alerts@example.com",
		[]string{"admin@example.com"},
		[]byte("Subject: test\r\n\r\nbackup failed\r\n"),
		time.Second,
		func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
	)
	if err != nil {
		t.Fatalf("sendSMTPMailWithDialer() error: %v", err)
	}
	select {
	case serverErr := <-serverResult:
		if serverErr != nil {
			t.Fatalf("SMTP server error: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SMTP server")
	}
	select {
	case message := <-deliveredMessage:
		if !strings.Contains(message, "Subject: test\n\nbackup failed") {
			t.Fatalf("delivered message = %q, want subject and body", message)
		}
	default:
		t.Fatal("SMTP server did not receive message data")
	}
}

func serveAuthenticatedSMTPTransaction(conn net.Conn, delivered chan<- string) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	if err := writeSMTPResponse(writer, "220 localhost ESMTP ready\r\n"); err != nil {
		return err
	}
	line, err := readSMTPLine(reader)
	if err != nil {
		return err
	}
	if line != "EHLO localhost" {
		return fmt.Errorf("first command = %q, want EHLO localhost", line)
	}
	if err := writeSMTPResponse(writer, "250-localhost\r\n250 AUTH PLAIN\r\n"); err != nil {
		return err
	}
	line, err = readSMTPLine(reader)
	if err != nil {
		return err
	}
	mechanism, encoded, ok := strings.Cut(line, " ")
	if !ok || mechanism != "AUTH" {
		return fmt.Errorf("auth command = %q, want AUTH PLAIN credentials", line)
	}
	mechanism, encoded, ok = strings.Cut(encoded, " ")
	if !ok || mechanism != "PLAIN" {
		return fmt.Errorf("auth command = %q, want AUTH PLAIN credentials", line)
	}
	credentials, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode AUTH PLAIN credentials: %w", err)
	}
	if string(credentials) != "\x00alerts\x00secret" {
		return fmt.Errorf("AUTH PLAIN credentials = %q, want configured credentials", credentials)
	}
	if err := writeSMTPResponse(writer, "235 authentication successful\r\n"); err != nil {
		return err
	}

	if err := expectSMTPCommand(reader, "MAIL FROM:<alerts@example.com>"); err != nil {
		return err
	}
	if err := writeSMTPResponse(writer, "250 sender accepted\r\n"); err != nil {
		return err
	}
	if err := expectSMTPCommand(reader, "RCPT TO:<admin@example.com>"); err != nil {
		return err
	}
	if err := writeSMTPResponse(writer, "250 recipient accepted\r\n"); err != nil {
		return err
	}
	if err := expectSMTPCommand(reader, "DATA"); err != nil {
		return err
	}
	if err := writeSMTPResponse(writer, "354 send message\r\n"); err != nil {
		return err
	}

	var lines []string
	for {
		line, err = readSMTPLine(reader)
		if err != nil {
			return err
		}
		if line == "." {
			break
		}
		lines = append(lines, line)
	}
	delivered <- strings.Join(lines, "\n")
	if err := writeSMTPResponse(writer, "250 message accepted\r\n"); err != nil {
		return err
	}
	if err := expectSMTPCommand(reader, "QUIT"); err != nil {
		return err
	}
	return writeSMTPResponse(writer, "221 closing connection\r\n")
}

func expectSMTPCommand(reader *bufio.Reader, want string) error {
	line, err := readSMTPLine(reader)
	if err != nil {
		return err
	}
	if line != want {
		return fmt.Errorf("SMTP command = %q, want %q", line, want)
	}
	return nil
}

func readSMTPLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func writeSMTPResponse(writer *bufio.Writer, response string) error {
	if _, err := writer.WriteString(response); err != nil {
		return err
	}
	return writer.Flush()
}
