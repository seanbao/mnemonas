package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"time"
)

// defaultSMTPTimeout bounds the complete SMTP transaction when the caller has no earlier deadline.
const defaultSMTPTimeout = 30 * time.Second

type smtpDialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

var sendSMTPMail = sendSMTPMailContext

func sendSMTPMailContext(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return sendSMTPMailWithDialer(
		ctx,
		addr,
		auth,
		from,
		to,
		msg,
		defaultSMTPTimeout,
		(&net.Dialer{}).DialContext,
	)
}

func sendSMTPMailWithDialer(
	ctx context.Context,
	addr string,
	auth smtp.Auth,
	from string,
	to []string,
	msg []byte,
	timeout time.Duration,
	dial smtpDialContextFunc,
) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if err != nil {
			if ctxErr := sendCtx.Err(); ctxErr != nil {
				err = ctxErr
				return
			}
			if deadline, ok := sendCtx.Deadline(); ok && !time.Now().Before(deadline) {
				err = context.DeadlineExceeded
			}
		}
	}()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	conn, err := dial(sendCtx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	deadline, _ := sendCtx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stopCancelInterrupt := context.AfterFunc(sendCtx, func() {
		_ = conn.Close()
	})
	defer stopCancelInterrupt()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Hello("localhost"); err != nil {
		return err
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
