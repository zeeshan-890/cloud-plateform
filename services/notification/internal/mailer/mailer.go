// Package mailer sends e-mail over SMTP (implicit TLS, STARTTLS or plain) and renders templates.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Config is the effective SMTP configuration.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	TLS      string // starttls (default) | tls | none
	// InsecureSkipVerify is for tests with self-signed servers only.
	InsecureSkipVerify bool
}

// Configured reports whether enough is set to attempt delivery.
func (c Config) Configured() bool { return c.Host != "" && c.From != "" }

// Message is one outgoing e-mail.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, cfg Config, m Message) error
}

// SMTP is the real sender.
type SMTP struct{ Timeout time.Duration }

// ErrNotConfigured is returned when SMTP settings are missing.
var ErrNotConfigured = errors.New("smtp is not configured (set SMTP_HOST/SMTP_FROM or platform settings)")

func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// ValidAddress checks an address and returns its bare form.
func ValidAddress(a string) (string, error) {
	p, err := mail.ParseAddress(strings.TrimSpace(a))
	if err != nil || strings.ContainsAny(a, "\r\n") {
		return "", errors.New("invalid e-mail address")
	}
	return p.Address, nil
}

// Build renders an RFC 5322 multipart/alternative message.
func Build(from string, m Message) ([]byte, string, string, error) {
	fromAddr, err := mail.ParseAddress(sanitizeHeader(from))
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid From: %w", err)
	}
	to, err := ValidAddress(m.To)
	if err != nil {
		return nil, "", "", err
	}
	boundary := fmt.Sprintf("jp-%d", time.Now().UnixNano())
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		fromAddr.String(), to, mimeQ(sanitizeHeader(m.Subject)), time.Now().UTC().Format(time.RFC1123Z))
	if m.HTML != "" {
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, crlf(m.Text))
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n--%s--\r\n", boundary, crlf(m.HTML), boundary)
	} else {
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", crlf(m.Text))
	}
	return b.Bytes(), fromAddr.Address, to, nil
}

func mimeQ(s string) string { return mime.QEncoding.Encode("utf-8", s) }

func crlf(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// Send delivers one message.
func (s SMTP) Send(ctx context.Context, cfg Config, m Message) error {
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	port := cfg.Port
	if port == "" {
		port = "587"
		if cfg.TLS == "tls" {
			port = "465"
		}
	}
	msg, from, to, err := Build(cfg.From, m)
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(cfg.Host, port)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify}

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if cfg.TLS == "tls" {
		conn = tls.Client(conn, tlsCfg)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if cfg.TLS != "tls" && cfg.TLS != "none" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS (set smtp_tls=none to allow plaintext)")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("server does not support AUTH")
		}
		// PlainAuth refuses to send credentials over an unencrypted connection to a non-local host.
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// InviteMessage renders the organization invitation e-mail (text + html).
func InviteMessage(to, dashboardURL, token, orgName, role, inviter string) Message {
	link := strings.TrimRight(dashboardURL, "/") + "/invite/accept?token=" + url.QueryEscape(token)
	if orgName == "" {
		orgName = "an organization"
	}
	who := "You have been invited"
	if inviter != "" {
		who = inviter + " invited you"
	}
	subject := "You're invited to join " + orgName + " on JP Cloud"
	text := fmt.Sprintf("%s to join %s on JP Cloud as %s.\n\nAccept the invitation:\n%s\n\nThis link is personal and expires in 7 days. If you did not expect this, ignore this e-mail.\n", who, orgName, role, link)
	h := fmt.Sprintf(`<!doctype html><html><body style="font-family:sans-serif"><p>%s to join <strong>%s</strong> on JP Cloud as <strong>%s</strong>.</p><p><a href="%s">Accept the invitation</a></p><p style="color:#666;font-size:12px">This link is personal and expires in 7 days. If you did not expect this, ignore this e-mail.<br>%s</p></body></html>`,
		html.EscapeString(who), html.EscapeString(orgName), html.EscapeString(role), html.EscapeString(link), html.EscapeString(link))
	return Message{To: to, Subject: subject, Text: text, HTML: h}
}
