package gorpc

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/rpc"

	"github.com/MeteorsLiu/rpc/adapter"
)

type RPCServerOption func(*GoRPCServer)

func WithClientCA(cert []byte) RPCServerOption {
	return func(gr *GoRPCServer) {
		if gr.tls == nil {
			gr.tls = &tls.Config{}
		}
		if gr.tls.ClientCAs == nil {
			gr.tls.ClientCAs = x509.NewCertPool()
			gr.tls.ClientAuth = tls.RequireAndVerifyClientCert
		}
		gr.tls.ClientCAs.AppendCertsFromPEM(cert)
	}
}

func WithServerCert(cert tls.Certificate) RPCServerOption {
	return func(gr *GoRPCServer) {
		if gr.tls == nil {
			gr.tls = &tls.Config{}
		}
		if gr.tls.Certificates == nil {
			gr.tls.Certificates = []tls.Certificate{}
		}
		gr.tls.Certificates = append(gr.tls.Certificates, cert)
	}
}

func WithTLSConfig(c *tls.Config) RPCServerOption {
	return func(gr *GoRPCServer) {
		gr.tls = c
	}
}

type GoRPCServer struct {
	*rpc.Server
	tls *tls.Config
}

func NewGoRPCServer(opts ...RPCServerOption) adapter.Server {
	s := &GoRPCServer{Server: rpc.NewServer()}
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

func (s *GoRPCServer) AddCert(cert []byte) {
	// lazily
	s.tls.ClientCAs.AppendCertsFromPEM(cert)
}
