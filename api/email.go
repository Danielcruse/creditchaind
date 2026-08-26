package api

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
)

// smtpConfigured reports whether live outbound email is available on
// this node. Same fail-closed convention as requireAdmin/
// networkSecret (api/admin.go, api/peer.go): read directly from the
// environment, no separate startup log, no config struct - an
// operator opts in by setting the vars, and every call site that
// wants to send mail checks this first and falls back to its
// existing testnet-placeholder behavior when it's false.
func smtpConfigured() bool {
	return os.Getenv("SMTP_HOST") != "" &&
		os.Getenv("SMTP_USERNAME") != "" &&
		os.Getenv("SMTP_PASSWORD") != "" &&
		os.Getenv("SMTP_FROM") != ""
}

// sendEmail sends a plain-text email via SMTP with STARTTLS,
// authenticated using SMTP_USERNAME/SMTP_PASSWORD. Uses the Go
// standard library only (net/smtp + crypto/tls) - no third-party mail
// dependency, consistent with this project's existing zero-unaudited-
// dependency convention. Callers MUST check smtpConfigured() first;
// this function itself also fails closed (returns an error) if called
// with any required env var unset, rather than silently no-op'ing, so
// a caller that forgets the check gets a loud error in its own logs
// instead of a silently-dropped email.
func sendEmail(to, subject, body string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	username := os.Getenv("SMTP_USERNAME")
	password := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")

	if host == "" || username == "" || password == "" || from == "" {
		return fmt.Errorf("email: SMTP not configured")
	}
	if port == "" {
		port = "587"
	}

	addr := net.JoinHostPort(host, port)
	auth := smtp.PlainAuth("", username, password, host)

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s\r\n",
		from, to, subject, body,
	)

	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("email: dial failed: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("email: starttls failed: %w", err)
		}
	}

	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("email: auth failed: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM failed: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("email: RCPT TO failed: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: DATA failed: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		w.Close()
		return fmt.Errorf("email: write body failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close body failed: %w", err)
	}

	return client.Quit()
}
