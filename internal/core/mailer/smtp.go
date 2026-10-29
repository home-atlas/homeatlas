package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client is provider-neutral SMTP. Disabled configuration never reads secrets or connects.
type Client struct {
	host, port, security, auth, username, password, helo string
	from                                                 *mail.Address
	tls                                                  *tls.Config
}

func FromEnv(getenv func(string) string) (*Client, error) {
	enabled := getenv("HOMEATLAS_SMTP_ENABLED")
	if enabled == "" {
		return nil, nil
	}
	on, err := strconv.ParseBool(enabled)
	if err != nil {
		return nil, fmt.Errorf("HOMEATLAS_SMTP_ENABLED must be true or false")
	}
	if !on {
		return nil, nil
	}
	c := &Client{host: strings.TrimSpace(getenv("HOMEATLAS_SMTP_HOST")), port: getenv("HOMEATLAS_SMTP_PORT"), security: getenv("HOMEATLAS_SMTP_TLS"), auth: getenv("HOMEATLAS_SMTP_AUTH"), username: getenv("HOMEATLAS_SMTP_USERNAME"), password: getenv("HOMEATLAS_SMTP_PASSWORD")}
	c.helo = getenv("HOMEATLAS_SMTP_HELO")
	if strings.ContainsAny(c.helo, "\r\n\t ") {
		return nil, fmt.Errorf("HOMEATLAS_SMTP_HELO must be a hostname")
	}
	if c.security == "" {
		c.security = "starttls"
	}
	if c.auth == "" {
		c.auth = "auto"
	}
	if c.port == "" {
		if c.security == "tls" {
			c.port = "465"
		} else {
			c.port = "587"
		}
	}
	port, err := strconv.Atoi(c.port)
	if c.host == "" || strings.ContainsAny(c.host, "/\\\r\n\t @") || (strings.Contains(c.host, ":") && net.ParseIP(c.host) == nil) || err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("SMTP requires a host (without scheme/port) and port 1..65535")
	}
	if c.security != "starttls" && c.security != "tls" && c.security != "none" {
		return nil, fmt.Errorf("HOMEATLAS_SMTP_TLS must be starttls, tls or none")
	}
	if c.auth != "auto" && c.auth != "plain" && c.auth != "login" && c.auth != "none" {
		return nil, fmt.Errorf("HOMEATLAS_SMTP_AUTH must be auto, plain, login or none")
	}
	passwordFile := getenv("HOMEATLAS_SMTP_PASSWORD_FILE")
	if passwordFile != "" {
		if c.password != "" {
			return nil, fmt.Errorf("use SMTP_PASSWORD or SMTP_PASSWORD_FILE, not both")
		}
		raw, err := os.ReadFile(passwordFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read HOMEATLAS_SMTP_PASSWORD_FILE")
		}
		c.password = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	}
	if c.auth == "auto" && c.username == "" && c.password == "" {
		c.auth = "none"
	}
	if c.auth != "none" && (c.username == "" || c.password == "") {
		return nil, fmt.Errorf("SMTP authentication requires username and password")
	}
	if c.auth == "none" && (c.username != "" || c.password != "") {
		return nil, fmt.Errorf("SMTP auth=none does not accept credentials")
	}
	if c.security == "none" {
		ip := net.ParseIP(c.host)
		if c.auth != "none" || (c.host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, fmt.Errorf("SMTP without TLS is limited to unauthenticated loopback relays")
		}
	}
	from := getenv("HOMEATLAS_SMTP_FROM")
	if strings.ContainsAny(from, "\r\n") {
		return nil, fmt.Errorf("invalid HOMEATLAS_SMTP_FROM")
	}
	c.from, err = mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("HOMEATLAS_SMTP_FROM must be a sender email, optionally with a display name")
	}
	serverName := getenv("HOMEATLAS_SMTP_SERVER_NAME")
	if serverName == "" {
		serverName = c.host
	}
	c.tls = &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if path := getenv("HOMEATLAS_SMTP_CA_FILE"); path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read HOMEATLAS_SMTP_CA_FILE")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("cannot load system CA certificates")
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("SMTP_CA_FILE contains no CA certificates")
		}
		c.tls.RootCAs = pool
	}
	return c, nil
}

func (c *Client) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return fmt.Errorf("invalid mail headers")
	}
	address, err := mail.ParseAddress(to)
	if err != nil || address.Address != to {
		return fmt.Errorf("invalid recipient")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(c.host, c.port))
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if c.security == "none" {
		addr, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok || !addr.IP.IsLoopback() {
			return fmt.Errorf("unencrypted SMTP relay must be loopback")
		}
	}
	if c.security == "tls" {
		secured := tls.Client(conn, c.tls.Clone())
		if err := secured.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secured
	}
	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if c.helo != "" {
		if err := client.Hello(c.helo); err != nil {
			return err
		}
	}
	if c.security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support required STARTTLS")
		}
		if err := client.StartTLS(c.tls.Clone()); err != nil {
			return err
		}
	}
	if c.auth != "none" {
		ok, methods := client.Extension("AUTH")
		if !ok {
			return fmt.Errorf("SMTP server does not offer authentication")
		}
		method := c.auth
		available := " " + strings.ToUpper(methods) + " "
		if method == "auto" {
			if strings.Contains(available, " PLAIN ") {
				method = "plain"
			} else if strings.Contains(available, " LOGIN ") {
				method = "login"
			} else {
				return fmt.Errorf("SMTP requires PLAIN or LOGIN authentication")
			}
		}
		if !strings.Contains(available, " "+strings.ToUpper(method)+" ") {
			return fmt.Errorf("SMTP server does not offer configured authentication")
		}
		if err := client.Auth(&passwordAuth{method: strings.ToUpper(method), username: c.username, password: c.password}); err != nil {
			return err
		}
	}
	if err := client.Mail(c.from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(address.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	var encoded bytes.Buffer
	domain := c.from.Address[strings.LastIndex(c.from.Address, "@")+1:]
	fmt.Fprintf(&encoded, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", c.from.String(), address.String(), mime.QEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z), rand.Text(), domain)
	qp := quotedprintable.NewWriter(&encoded)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	if err := qp.Close(); err != nil {
		return err
	}
	if _, err := w.Write(encoded.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// A successful DATA reply means accepted by the relay; QUIT failure cannot undo delivery.
	_ = client.Quit()
	return nil
}

type passwordAuth struct {
	method, username, password string
	step                       int
}

func (a *passwordAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, fmt.Errorf("SMTP credentials require TLS")
	}
	if a.method == "PLAIN" {
		return a.method, []byte("\x00" + a.username + "\x00" + a.password), nil
	}
	return "LOGIN", nil, nil
}
func (a *passwordAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if a.method != "LOGIN" {
		return nil, fmt.Errorf("unexpected SMTP challenge")
	}
	a.step++
	if a.step == 1 {
		return []byte(a.username), nil
	}
	if a.step == 2 {
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("too many SMTP authentication challenges")
}
