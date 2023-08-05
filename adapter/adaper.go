package adapter

import (
	"io"
	"net"
	"net/http"
	"net/rpc"
)

type Client interface {
	Call(serviceMethod string, args any, reply any) error
	io.Closer
}

type Server interface {
	Accept(lis net.Listener)
	HandleHTTP(rpcPath, debugPath string)
	Register(rcvr any) error
	RegisterName(name string, rcvr any) error
	ServeCodec(codec rpc.ServerCodec)
	ServeConn(conn io.ReadWriteCloser)
	ServeHTTP(w http.ResponseWriter, req *http.Request)
	ServeRequest(codec rpc.ServerCodec) error
}
