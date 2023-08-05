package gorpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"runtime"
	"sync"
	"time"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/alphadose/haxmap"
)

var (
	ErrConnect = fmt.Errorf("fail to connect to the target address")
)

type ClientOption func(*GoRPCClient)

type DialerFunc func() (io.ReadWriteCloser, error)

func WithServerCert(cert []byte) ClientOption {
	return func(gr *GoRPCClient) {
		if gr.tls == nil {
			gr.tls = &tls.Config{}
		}
		if gr.tls.RootCAs == nil {
			gr.tls.RootCAs = x509.NewCertPool()
		}
		gr.tls.RootCAs.AppendCertsFromPEM(cert)
	}
}

func WithClientCert(cert tls.Certificate) ClientOption {
	return func(gr *GoRPCClient) {
		if gr.tls == nil {
			gr.tls = &tls.Config{}
		}
		if gr.tls.Certificates == nil {
			gr.tls.Certificates = []tls.Certificate{}
		}
		gr.tls.Certificates = append(gr.tls.Certificates, cert)
	}
}

func WithClientTLSConfig(c *tls.Config) ClientOption {
	return func(gr *GoRPCClient) {
		gr.tls = c
	}
}

func DefaultDialerFunc(address string) DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		return net.DialTimeout("tcp", address, 30*time.Second)
	}
}

func DefaultTLSDialerFunc(address string, c *tls.Config) DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", address, c)
	}
}

type conn struct {
	c   *rpc.Client
	err error
	sync.Mutex
}

// connPool is a thread-safe FIFO queue
// enqueue will be pushed into the tail,
// dequeue will be poped from the head.
type connPool struct {
	pushMu     sync.Mutex
	connWarper DialerFunc
	conns      *haxmap.Map[int, *conn]
}

func newConn(c io.ReadWriteCloser) *conn {
	cc := &conn{}
	if c != nil {
		cc.SetConn(c)
	}
	runtime.SetFinalizer(cc, func(rc *conn) {
		if rc.c != nil {
			rc.c.Close()
		}
	})
	return cc
}

func (c *conn) SetConn(cc io.ReadWriteCloser) {
	c.c = rpc.NewClient(cc)
}

func (c *conn) Reconnect(dialer DialerFunc, onSuccess func(*conn), onFail func(*conn)) error {
	cc, err := dialer()
	if err != nil {
		c.err = err
		if onFail != nil {
			onFail(c)
		}
		return err
	}
	c.err = nil
	c.SetConn(cc)
	if onSuccess != nil {
		onSuccess(c)
	}
	return nil
}

func newConnPool(c DialerFunc) *connPool {
	cp := &connPool{
		connWarper: c,
		conns:      haxmap.New[int, *conn](),
	}
	newc, err := c()
	if err != nil {
		return nil
	}
	cp.conns.Set(0, newConn(newc))
	return cp
}

func (c *connPool) new() (id int, cn *conn, err error) {

	c.pushMu.Lock()
	id = int(c.conns.Len())
	cn = newConn(nil)
	cn.Lock()
	c.conns.Set(id, cn)
	c.pushMu.Unlock()

	newc, err := c.connWarper()
	if err != nil {
		cn.err = err
		cn.Unlock()
		return
	}
	cn.SetConn(newc)
	return
}

func (c *connPool) Get() (ok bool, id int, cc *rpc.Client) {
	var cn *conn
	var err error
	for id = 0; id < int(c.conns.Len()); id++ {
		cn, ok = c.conns.Get(id)
		if ok && cn.TryLock() {
			if cn.err != nil {
				if err := cn.Reconnect(c.connWarper, nil, func(cnn *conn) {
					cnn.Unlock()
				}); err != nil {
					continue
				}
			}
			cc = cn.c
			return
		}
	}
	// no connections
	id, cn, err = c.new()
	ok = err == nil
	cc = cn.c

	return
}

func (c *connPool) Remove(id int) {
	c.conns.Del(id)
}

func (c *connPool) Put(id int, err ...error) {
	cn, ok := c.conns.Get(id)
	if !ok {
		return
	}
	if len(err) > 0 && err[0] != nil {
		cn.err = err[0]
		cn.c.Close()
		cn.Reconnect(c.connWarper, nil, nil)
	}
	cn.Unlock()
}

func (c *connPool) Close() error {
	var err error
	c.conns.ForEach(func(id int, c *conn) bool {
		if e := c.c.Close(); e != nil {
			err = e
		}
		return true
	})
	return err
}

type GoRPCClient struct {
	conn *connPool
	tls  *tls.Config
}

func NewGoRPCClient(address string, opts ...ClientOption) adapter.Client {
	if address == "" {
		return nil
	}
	cc := &GoRPCClient{}
	for _, o := range opts {
		o(cc)
	}
	switch {
	case cc.tls == nil && cc.conn == nil:
		cc.conn = newConnPool(DefaultDialerFunc(address))
	case cc.tls != nil && cc.conn == nil:
		cc.conn = newConnPool(DefaultTLSDialerFunc(address, cc.tls))
	}

	return cc
}

func (g *GoRPCClient) Call(serviceMethod string, args any, reply any) (err error) {
	ok, id, conn := g.conn.Get()
	if !ok {
		err = ErrConnect
		return
	}
	if err = conn.Call(serviceMethod, args, reply); err != nil {
		g.conn.Put(id, err)
		return
	}
	g.conn.Put(id)
	return nil
}

func (g *GoRPCClient) Close() error {
	return g.conn.Close()
}
