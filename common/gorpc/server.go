package gorpc

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/MeteorsLiu/rpc/common/gorpc/rpcgo"
)

type RPCServerOption func(*GoRPCServer)

func WithClientCA(cert []byte) RPCServerOption {
	return func(gr *GoRPCServer) {
		if gr.tls == nil {
			gr.tls = &tls.Config{
				MinVersion: tls.VersionTLS13,
			}
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
			gr.tls = &tls.Config{
				MinVersion: tls.VersionTLS13,
			}
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

func WithServerMiddleware(m adapter.ServerMiddleware) RPCServerOption {
	return func(gr *GoRPCServer) {
		gr.Server.Middlewares = append(gr.Server.Middlewares, m)
	}
}

func WithServerFinalizer(m adapter.ServerFinalizer) RPCServerOption {
	return func(gr *GoRPCServer) {
		gr.Server.Finalizers = append(gr.Server.Finalizers, m)
	}
}

type GoRPCServer struct {
	*rpcgo.Server
	tls *tls.Config
}

func NewGoRPCServer(opts ...RPCServerOption) adapter.Server {
	s := &GoRPCServer{Server: rpcgo.NewServer()}
	for _, o := range opts {
		o(s)
	}

	return s
}

func (s *GoRPCServer) Accept(l net.Listener) {
	if s.tls != nil {
		ll := tls.NewListener(l, s.tls)
		for {
			conn, err := ll.Accept()
			if err != nil {
				log.Print("rpc.Serve: tls accept:", err.Error())
				return
			}
			go s.ServeConn(conn)
		}
	} else {
		for {
			conn, err := l.Accept()
			if err != nil {
				log.Print("rpc.Serve: accept:", err.Error())
				return
			}
			go s.ServeConn(conn)
		}
	}
}

func (s *GoRPCServer) AddCert(cert []byte) {
	// lazily
	s.tls.ClientCAs.AppendCertsFromPEM(cert)
}
