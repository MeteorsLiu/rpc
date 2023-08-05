package gorpc

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/rpc"

	"github.com/MeteorsLiu/rpc/adapter"
)

type ServerOption func(*GoRPCServer)

func WithClientTLSCert(cert string) ServerOption {
	return func(gr *GoRPCServer) {
		if gr.tls == nil {
			gr.tls = &tls.Config{}
		}
		if gr.tls.ClientCAs == nil {
			gr.tls.ClientCAs = x509.NewCertPool()
		}
		gr.tls.ClientCAs.AppendCertsFromPEM([]byte(cert))
	}
}

func WithTLSConfig(c *tls.Config) ServerOption {
	return func(gr *GoRPCServer) {
		gr.tls = c
	}
}

type GoRPCServer struct {
	*rpc.Server
	tls *tls.Config
}

func NewGoRPCServer(opts ...ServerOption) adapter.Server {
	s := &GoRPCServer{rpc.NewServer(), nil}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *GoRPCServer) Accept(l net.Listener) {
	if s.tls != nil {
		s.Server.Accept(tls.NewListener(l, s.tls))
	} else {
		s.Server.Accept(l)
	}
}
