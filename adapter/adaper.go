package adapter

import (
	"io"
	"net"
)

type DialerFunc func() (io.ReadWriteCloser, error)
type ServerMiddleware func(methodName string, args any) error
type ServerFinalizer func(err error, methodName string, args, reply any)
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
	ServeCodec(codec ServerCodec)
	ServeConn(conn io.ReadWriteCloser)
	ServeRequest(codec ServerCodec) error
}

// Request is a header written before every RPC call. It is used internally
// but documented here as an aid to debugging, such as when analyzing
// network traffic.
type Request struct {
	ServiceMethod string   // format: "Service.Method"
	Seq           uint64   // sequence number chosen by client
	Next          *Request // for free list in Server
}

// Response is a header written before every RPC return. It is used internally
// but documented here as an aid to debugging, such as when analyzing
// network traffic.
type Response struct {
	ServiceMethod string    // echoes that of the Request
	Seq           uint64    // echoes that of the request
	Error         string    // error, if any.
	Next          *Response // for free list in Server
}
type ServerCodec interface {
	ReadRequestHeader(*Request) error
	ReadRequestBody(any) error
	WriteResponse(*Response, any) error

	// Close can be called multiple times and must be idempotent.
	Close() error
}
