package adapter

import (
	"io"
	"net"
	"net/rpc"
)

type DialerFunc func() (io.ReadWriteCloser, error)

type Client interface {
	SetDialer(dialer DialerFunc)
	SetRPCServer(address string) error
	CallWithConn(conn io.ReadWriteCloser, serviceMethod string, args any, reply any) error
	Call(serviceMethod string, args any, reply any) error
	io.Closer
}

type Server interface {
	AddCert(cert []byte)
	Accept(lis net.Listener)
	Register(rcvr any) error
	RegisterName(name string, rcvr any) error
	ServeCodec(codec rpc.ServerCodec)
	ServeConn(conn io.ReadWriteCloser)
	ServeRequest(codec rpc.ServerCodec) error
}
