package application

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/url"

	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

func ParseOrigin(publicURL string) (*url.URL, error) {
	origin, err := url.Parse(publicURL)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, fmt.Errorf("public URL must be an http(s) origin without a path")
	}
	loopback := origin.Hostname() == "localhost"
	if ip := net.ParseIP(origin.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if origin.Scheme == "http" && !loopback {
		return nil, fmt.Errorf("public URL requires HTTPS outside loopback")
	}
	return origin, nil
}
func New(ctx context.Context, store Repository, config Config) (*Application, error) {
	origin, err := ParseOrigin(config.PublicURL)
	if err != nil {
		return nil, err
	}
	s := &Application{store: store, origin: origin, capabilities: append([]string(nil), config.DelegableCapabilities...), ownerCapabilities: append([]string(nil), config.OwnerCapabilities...), work: make(chan struct{}, 2), modules: config.Modules}
	if err := s.prepareSetupCode(ctx, config.SetupCodePath); err != nil {
		return nil, err
	}
	s.dummyHash, err = domain.HashPassword(rand.Text())
	if err == nil && config.Mailer != nil {
		s.mailer = config.Mailer
		s.mailRequests = make(chan string, 16)
		mailCtx, cancel := context.WithCancel(ctx)
		s.mailCancel = cancel
		for range 2 {
			s.mailWorkers.Go(func() { s.recoveryMailWorker(mailCtx) })
		}
	}
	return s, err
}
func (s *Application) SMTPEnabled() bool { return s.mailer != nil }
