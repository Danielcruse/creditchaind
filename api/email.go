package api

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
)

// smtpConfiguredViaEnv reports whether live outbound email is
// available via the original EnvironmentFile-based setup. Same
// fail-closed convention as requireAdmin/networkSecret (api/admin.go,
// api/peer.go): read directly from the environment, no separate
// startup log, no config struct.
func smtpConfiguredViaEnv() bool {
	return os.Getenv("SMTP_HOST") != "" &&
		os.Getenv("SMTP_USERNAME") != "" &&
		os.Getenv("SMTP_PASSWORD") != "" &&
		os.Getenv("SMTP_FROM") != ""
}

// smtpConfigured reports whether live outbound email is available on
// this node, from EITHER source: the original env vars, or the
// admin-page-submitted config (api/smtp_admin_config.go). An operator
// opts in by setting one or the other, and every call site that wants
// to send mail checks this first and falls back to its existing
// testnet-placeholder behavior when it's false.
func smtpConfigured() bool {
	if smtpConfiguredViaEnv() {
		return true
	}
	_, ok := loadSmtpAdminConfig()
	return ok
}

// effectiveSmtpConfig resolves the live config to use, preferring the
// env vars (operator-controlled, harder for an attacker with only
// admin-page access to silently redirect mail through) over the
// admin-submitted file, and falls back to the file only when the env
// vars are entirely absent.
func effectiveSmtpConfig() (host, port, username, password, from string, ok bool) {
	if smtpConfiguredViaEnv() {
		return os.Getenv("SMTP_HOST"), os.Getenv("SMTP_PORT"), os.Getenv("SMTP_USERNAME"),
			os.Getenv("SMTP_PASSWORD"), os.Getenv("SMTP_FROM"), true
	}
	cfg, ok := loadSmtpAdminConfig()
	if !ok {
		return "", "", "", "", "", false
	}
	return cfg.Host, cfg.Port, cfg.Username, cfg.Password, cfg.From, true
}

// sendEmail sends a plain-text email via SMTP with STARTTLS,
// authenticated using the resolved username/password from
// effectiveSmtpConfig. Uses the Go standard library only (net/smtp +
// crypto/tls) - no third-party mail dependency, consistent with this
// project's existing zero-unaudited-dependency convention. Callers
// MUST check smtpConfigured() first; this function itself also fails
// closed (returns an error) if called with no config resolved, rather
// than silently no-op'ing, so a caller that forgets the check gets a
// loud error in its own logs instead of a silently-dropped email.
func sendEmail(to, subject, body string) error {
	host, port, username, password, from, ok := effectiveSmtpConfig()
	if !ok {
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
