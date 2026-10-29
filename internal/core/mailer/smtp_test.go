package mailer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func config() map[string]string {
	return map[string]string{
		"HOMEATLAS_SMTP_ENABLED": "true", "HOMEATLAS_SMTP_HOST": "127.0.0.1", "HOMEATLAS_SMTP_FROM": "HomeAtlas <home@example.test>",
		"HOMEATLAS_SMTP_USERNAME": "smtp-user", "HOMEATLAS_SMTP_PASSWORD": "smtp-password",
	}
}
func configured(t *testing.T, values map[string]string) *Client {
	t.Helper()
	c, err := FromEnv(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDisabledSMTPDoesNotReadSecretsOrValidatePlaceholders(t *testing.T) {
	for _, enabled := range []string{"", "false"} {
		values := config()
		values["HOMEATLAS_SMTP_ENABLED"] = enabled
		values["HOMEATLAS_SMTP_PASSWORD_FILE"] = "/missing/secret"
		values["HOMEATLAS_SMTP_PORT"] = "placeholder"
		c, err := FromEnv(func(key string) string { return values[key] })
		if err != nil || c != nil {
			t.Fatal("disabled SMTP must be inert", err)
		}
	}
}

func TestSMTPConfigurationValidationAndSecretFile(t *testing.T) {
	for key, value := range map[string]string{
		"HOMEATLAS_SMTP_ENABLED": "maybe", "HOMEATLAS_SMTP_HOST": "smtp://host:587", "HOMEATLAS_SMTP_PORT": "0",
		"HOMEATLAS_SMTP_TLS": "optional", "HOMEATLAS_SMTP_AUTH": "oauth", "HOMEATLAS_SMTP_FROM": "me@example.test\r\nBcc: bad@example.test",
		"HOMEATLAS_SMTP_PASSWORD": "", "HOMEATLAS_SMTP_CA_FILE": "/missing/ca", "HOMEATLAS_SMTP_HELO": "bad\r\nMAIL FROM:x",
	} {
		t.Run(key, func(t *testing.T) {
			values := config()
			values[key] = value
			if _, err := FromEnv(func(k string) string { return values[k] }); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	for _, host := range []string{"mail.internal", "192.0.2.10"} {
		values := config()
		values["HOMEATLAS_SMTP_HOST"] = host
		values["HOMEATLAS_SMTP_TLS"] = "none"
		values["HOMEATLAS_SMTP_AUTH"] = "none"
		values["HOMEATLAS_SMTP_USERNAME"] = ""
		values["HOMEATLAS_SMTP_PASSWORD"] = ""
		if _, err := FromEnv(func(k string) string { return values[k] }); err == nil {
			t.Fatal("remote plaintext accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values := config()
	values["HOMEATLAS_SMTP_PASSWORD_FILE"] = path
	if _, err := FromEnv(func(k string) string { return values[k] }); err == nil {
		t.Fatal("two password sources accepted")
	}
	values["HOMEATLAS_SMTP_PASSWORD"] = ""
	if c := configured(t, values); c.password != "file-secret" || c.port != "587" || c.security != "starttls" {
		t.Fatal("incorrect defaults/secret")
	}
	values["HOMEATLAS_SMTP_TLS"] = "tls"
	if c := configured(t, values); c.port != "465" {
		t.Fatal("incorrect implicit TLS default")
	}
}

type relay struct {
	listener net.Listener
	cert     tls.Certificate
	pem      []byte
	messages chan string
	commands chan string
	workers  sync.WaitGroup
	security string
	reject   bool
}

func newRelay(t *testing.T, security string, reject bool) *relay {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtp.example.test"}, DNSNames: []string{"smtp.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	r := &relay{cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), messages: make(chan string, 8), commands: make(chan string, 64), security: security, reject: reject}
	r.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r.workers.Go(func() {
		for {
			conn, err := r.listener.Accept()
			if err != nil {
				return
			}
			r.workers.Go(func() { r.serve(conn) })
		}
	})
	t.Cleanup(func() { r.listener.Close(); r.workers.Wait() })
	return r
}
func (r *relay) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	secure := r.security == "tls"
	if secure {
		conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{r.cert}, MinVersion: tls.VersionTLS12})
	}
	p := textproto.NewConn(conn)
	defer p.Close()
	if err := p.PrintfLine("220 test relay"); err != nil {
		return
	}
	for {
		line, err := p.ReadLine()
		if err != nil {
			return
		}
		r.commands <- line
		switch {
		case strings.HasPrefix(line, "EHLO"):
			if r.security == "starttls" && !secure {
				p.PrintfLine("250-test\r\n250-STARTTLS\r\n250 AUTH PLAIN LOGIN")
			} else {
				p.PrintfLine("250-test\r\n250 AUTH PLAIN LOGIN")
			}
		case line == "STARTTLS":
			p.PrintfLine("220 ready")
			conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{r.cert}, MinVersion: tls.VersionTLS12})
			p = textproto.NewConn(conn)
			secure = true
		case strings.HasPrefix(line, "AUTH PLAIN "):
			if !secure {
				p.PrintfLine("538 TLS required")
				continue
			}
			p.PrintfLine("235 authenticated")
		case line == "AUTH LOGIN":
			if !secure {
				p.PrintfLine("538 TLS required")
				continue
			}
			p.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, err := p.ReadLine()
			if err != nil {
				return
			}
			p.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
			pw, err := p.ReadLine()
			if err != nil {
				return
			}
			r.commands <- "LOGIN " + u + " " + pw
			p.PrintfLine("235 authenticated")
		case strings.HasPrefix(line, "MAIL FROM:"):
			p.PrintfLine("250 sender")
		case strings.HasPrefix(line, "RCPT TO:"):
			if r.reject {
				p.PrintfLine("550 rejected")
			} else {
				p.PrintfLine("250 recipient")
			}
		case line == "DATA":
			p.PrintfLine("354 data")
			raw, err := p.ReadDotBytes()
			if err != nil {
				return
			}
			r.messages <- string(raw)
			p.PrintfLine("250 queued")
		case line == "QUIT":
			p.PrintfLine("221 bye")
			return
		default:
			p.PrintfLine("500 unexpected")
		}
	}
}
func (r *relay) config(t *testing.T) map[string]string {
	t.Helper()
	v := config()
	v["HOMEATLAS_SMTP_PORT"] = strings.Split(r.listener.Addr().String(), ":")[1]
	v["HOMEATLAS_SMTP_TLS"] = r.security
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, r.pem, 0600); err != nil {
		t.Fatal(err)
	}
	v["HOMEATLAS_SMTP_CA_FILE"] = path
	if r.security == "none" {
		v["HOMEATLAS_SMTP_USERNAME"] = ""
		v["HOMEATLAS_SMTP_PASSWORD"] = ""
		v["HOMEATLAS_SMTP_AUTH"] = "none"
	}
	return v
}

func TestSMTPWithCommercialAndSelfHostedTransportModes(t *testing.T) {
	for _, test := range []struct{ security, auth string }{{"starttls", "plain"}, {"starttls", "login"}, {"tls", "auto"}, {"starttls", "none"}, {"none", "none"}} {
		t.Run(test.security+"/"+test.auth, func(t *testing.T) {
			r := newRelay(t, test.security, false)
			values := r.config(t)
			values["HOMEATLAS_SMTP_AUTH"] = test.auth
			values["HOMEATLAS_SMTP_HELO"] = "home.example.test"
			if test.auth == "none" {
				values["HOMEATLAS_SMTP_USERNAME"] = ""
				values["HOMEATLAS_SMTP_PASSWORD"] = ""
			}
			c := configured(t, values)
			if err := c.Send(context.Background(), "person@example.test", "Invitación y verificación", "Abre https://home.example.test/join#private-test-code\nInformación privada."); err != nil {
				t.Fatal(err)
			}
			select {
			case raw := <-r.messages:
				message, err := mail.ReadMessage(strings.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
				if err != nil || subject != "Invitación y verificación" {
					t.Fatal("invalid UTF-8 subject")
				}
				body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
				if err != nil || !strings.Contains(string(body), "Información privada.") || !strings.Contains(string(body), "#private-test-code") {
					t.Fatal("invalid UTF-8 body")
				}
				if message.Header.Get("From") == "" || message.Header.Get("Message-ID") == "" {
					t.Fatal("missing message headers")
				}
			default:
				t.Fatal("SMTP accepted no mail")
			}
		})
	}
}

func TestSMTPRejectsDowngradeUntrustedCAAndInjection(t *testing.T) {
	for _, kind := range []string{"no-starttls", "untrusted", "wrong-hostname", "recipient-rejected", "headers"} {
		t.Run(kind, func(t *testing.T) {
			security := "starttls"
			if kind == "no-starttls" {
				security = "none"
			}
			r := newRelay(t, security, kind == "recipient-rejected")
			values := r.config(t)
			values["HOMEATLAS_SMTP_TLS"] = "starttls"
			if kind == "untrusted" {
				values["HOMEATLAS_SMTP_CA_FILE"] = ""
			}
			if kind == "wrong-hostname" {
				values["HOMEATLAS_SMTP_SERVER_NAME"] = "wrong.example.test"
			}
			c := configured(t, values)
			subject := "Test"
			if kind == "headers" {
				subject = "Test\r\nBcc:bad@example.test"
			}
			if err := c.Send(context.Background(), "person@example.test", subject, "private"); err == nil {
				t.Fatal("unsafe SMTP accepted")
			}
			select {
			case <-r.messages:
				t.Fatal("failed SMTP leaked message")
			default:
			}
		})
	}
}

func TestSMTPContextCancellation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(time.Second))
		io.Copy(io.Discard, conn)
	}()
	v := config()
	v["HOMEATLAS_SMTP_PORT"] = strings.Split(l.Addr().String(), ":")[1]
	c := configured(t, v)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Send(ctx, "person@example.test", "Test", "private"); err == nil {
		t.Fatal("SMTP ignored cancellation")
	}
	<-done
}
