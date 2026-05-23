package server

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// Server runs DNS over UDP and TCP on the same address.
type Server struct {
	addr    string
	handler dns.Handler
	log     *zap.Logger
	udp     *dns.Server
	tcp     *dns.Server
}

func New(addr string, handler dns.Handler, log *zap.Logger) *Server {
	return &Server{addr: addr, handler: handler, log: log}
}

// Start binds the ports and begins serving. It returns once both listeners are ready.
func (s *Server) Start() error {
	udpReady := make(chan struct{})
	tcpReady := make(chan struct{})
	errCh := make(chan error, 2)

	udpPC, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", s.addr, err)
	}
	tcpL, err := net.Listen("tcp", s.addr)
	if err != nil {
		udpPC.Close()
		return fmt.Errorf("tcp listen %s: %w", s.addr, err)
	}

	s.udp = &dns.Server{
		PacketConn:        udpPC,
		Net:               "udp",
		Handler:           s.handler,
		NotifyStartedFunc: func() { close(udpReady) },
	}
	s.tcp = &dns.Server{
		Listener:          tcpL,
		Net:               "tcp",
		Handler:           s.handler,
		NotifyStartedFunc: func() { close(tcpReady) },
	}

	go func() { errCh <- s.udp.ActivateAndServe() }()
	go func() { errCh <- s.tcp.ActivateAndServe() }()

	<-udpReady
	<-tcpReady

	s.log.Info("listening", zap.String("addr", s.addr))
	return nil
}

// Shutdown gracefully stops both servers, respecting ctx deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	stop := func(srv *dns.Server, name string) {
		defer wg.Done()
		if err := srv.ShutdownContext(ctx); err != nil {
			errs <- fmt.Errorf("%s: %w", name, err)
		}
	}

	wg.Add(2)
	go stop(s.udp, "udp")
	go stop(s.tcp, "tcp")
	wg.Wait()
	close(errs)

	for err := range errs {
		return err
	}
	return nil
}
