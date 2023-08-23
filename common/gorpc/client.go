package gorpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MeteorsLiu/rpc/adapter"
)

var (
	ErrCleaned     = fmt.Errorf("connection has been cleaned")
	ErrConnect     = fmt.Errorf("fail to connect to the target address")
	ErrInitialized = fmt.Errorf("fail to initilize the connection pool")
	ErrNoServer    = fmt.Errorf("no rpc server")
)

func IsRPCServerError(err error) bool {
	_, ok := err.(rpc.ServerError)
	return ok
}

type RPCClientOption func(*GoRPCClient)

func WithCACert(cert []byte) RPCClientOption {
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

func WithClientCert(cert tls.Certificate) RPCClientOption {
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

func WithClientDialer(dialer adapter.DialerFunc) RPCClientOption {
	return func(gr *GoRPCClient) {
		var err error
		gr.conn, err = newConnPool(dialer)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func WithClientTLSConfig(c *tls.Config) RPCClientOption {
	return func(gr *GoRPCClient) {
		gr.tls = c
	}
}

func DefaultDialerFunc(address string) adapter.DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		return net.DialTimeout("tcp", address, 30*time.Second)
	}
}

func DefaultTLSDialerFunc(address string, c *tls.Config) adapter.DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", address, c)
	}
}

type conn struct {
	id  int
	c   *rpc.Client
	err error
	sync.Mutex
}

// connPool is a thread-safe FIFO queue
// enqueue will be pushed into the tail,
// dequeue will be poped from the head.
type connPool struct {
	updateMu   sync.RWMutex
	resizeMu   sync.RWMutex
	close      context.Context
	doClose    context.CancelFunc
	seq        atomic.Int64
	cnt        atomic.Int64
	connWarper adapter.DialerFunc
	conns      []*conn
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
	c.c = jsonrpc.NewClient(cc)
}

func (c *conn) Reconnect(dialer adapter.DialerFunc, onSuccess func(*conn), onFail func(*conn)) error {
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

func newConnPool(c adapter.DialerFunc) (*connPool, error) {
	cp := &connPool{
		connWarper: c,
	}
	cp.close, cp.doClose = context.WithCancel(context.Background())
	newc, err := c()
	if err != nil {
		return nil, err
	}

	cp.conns = append(cp.conns, newConn(newc))

	go func() {
		// the reason why the default cleaning period is 155s is that
		// go's default keepalive idle is 15s and 9 rounds
		// so the keepalive timeout is 150s, 5s for connection died.
		ticker := time.NewTicker(155 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// resized
				if len(cp.conns) > 128 {
					cp.forEachLockFree(func(i int, c *conn) bool {
						if c.TryLock() {
							c.err = ErrCleaned
							c.c.Close()
							c.Unlock()
						}
						return true
					})
				} else {
					cp.forEachLockFree(func(i int, c *conn) bool {
						if c.err != nil && c.TryLock() {
							c.Reconnect(cp.dial, nil, nil)
							c.Unlock()
						}
						return true
					})
				}
			case <-cp.close.Done():
				return
			}
		}
	}()
	return cp, nil
}

func (c *connPool) dial() (io.ReadWriteCloser, error) {
	var dialerFunc adapter.DialerFunc
	c.updateMu.RLock()
	dialerFunc = c.connWarper
	c.updateMu.RUnlock()
	return dialerFunc()
}

func (c *connPool) push(cn *conn) (id int) {
	c.resizeMu.Lock()
	c.conns = append(c.conns, cn)
	c.resizeMu.Unlock()

	id = int(c.seq.Add(1))
	cn.id = id
	return
}

func (c *connPool) pop() (id int, cn *conn) {
	seq := c.seq.Load()
	if seq > 0 {
		id = int((c.cnt.Add(1) - 1) % seq)
	}
	cc := c.conns[id]
	if cc != nil && cc.TryLock() {
		cn = cc
	}
	return
}

func (c *connPool) new() (id int, cn *conn, err error) {
	cn = newConn(nil)
	cn.Lock()
	id = c.push(cn)
	newc, err := c.dial()
	if err != nil {
		cn.err = err
		cn.Unlock()
		return
	}
	cn.SetConn(newc)
	return
}

func (c *connPool) forEach(f func(int, *conn) bool) {
	c.resizeMu.RLock()
	defer c.resizeMu.RUnlock()
	// don't use range, because range will do a large copy
	for id := 0; id < len(c.conns); id++ {
		current := c.conns[id]
		if current == nil || !f(id, current) {
			return
		}
	}
}

func (c *connPool) forEachLockFree(f func(int, *conn) bool) {
	// don't use range, because range will do a large copy
	for id := 0; id < int(c.seq.Load())+1; id++ {
		current := c.conns[id]
		if current == nil || !f(id, current) {
			return
		}
	}
}

func (c *connPool) Get() (ok bool, id int, cc *rpc.Client) {
	var cn *conn
	var err error

	id, cn = c.pop()
	if cn == nil {
		c.forEach(func(i int, cn *conn) bool {
			if cn.TryLock() {
				if cn.err != nil {
					if err := cn.Reconnect(c.dial, nil, func(cnn *conn) {
						cnn.Unlock()
					}); err != nil {
						return true
					}
				}
				id = i
				cc = cn.c
				ok = true
				return false
			}
			return true
		})
	} else {
		cc = cn.c
		ok = true
	}

	if ok {
		return
	}
	// no connections
	id, cn, err = c.new()
	ok = err == nil
	cc = cn.c
	return
}

func (c *connPool) Put(id int, err ...error) {
	cn := c.conns[id]
	if cn == nil {
		return
	}

	if len(err) > 0 && err[0] != nil {
		cn.err = err[0]
		cn.c.Close()
		cn.Reconnect(c.dial, nil, nil)
	}
	cn.Unlock()
}

func (c *connPool) setServer(dialer adapter.DialerFunc) {
	c.updateMu.Lock()
	defer c.updateMu.Unlock()

	c.connWarper = dialer
}

func (c *connPool) Close() error {
	var err error
	c.doClose()
	c.forEach(func(id int, c *conn) bool {
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

func NewGoRPCClient(address string, opts ...RPCClientOption) (adapter.Client, error) {
	if address == "" {
		return nil, ErrNoServer
	}
	cc := &GoRPCClient{}
	for _, o := range opts {
		o(cc)
	}
	var err error
	switch {
	case cc.tls == nil && cc.conn == nil:
		cc.conn, err = newConnPool(DefaultDialerFunc(address))
	case cc.tls != nil && cc.conn == nil:
		cc.conn, err = newConnPool(DefaultTLSDialerFunc(address, cc.tls))
	}
	if cc.conn == nil {
		return nil, err
	}

	return cc, nil
}

func (g *GoRPCClient) Call(serviceMethod string, args any, reply any) (err error) {
	ok, id, conn := g.conn.Get()
	if !ok {
		err = ErrConnect
		return
	}
retry:
	if err = conn.Call(serviceMethod, args, reply); err != nil {
		// we only need reconnect when the connection is broken.
		if !errors.Is(err, rpc.ErrShutdown) && !IsRPCServerError(err) {
			g.conn.Put(id, err)
			ok, id, conn = g.conn.Get()
			if !ok {
				return
			}
			goto retry
		}
	}
	g.conn.Put(id)
	return
}

func (g *GoRPCClient) CallWithConn(conn io.ReadWriteCloser, serviceMethod string, args any, reply any) error {
	// don't use conn pool
	nrpc := jsonrpc.NewClient(conn)
	defer nrpc.Close()
	return nrpc.Call(serviceMethod, args, reply)
}

func (g *GoRPCClient) Close() error {
	return g.conn.Close()
}

func (g *GoRPCClient) SetRPCServer(address string) error {
	if address == "" {
		return ErrNoServer
	}
	if g.tls != nil {
		g.conn.setServer(DefaultTLSDialerFunc(address, g.tls))
	} else {
		g.conn.setServer(DefaultDialerFunc(address))
	}
	return nil
}

func (g *GoRPCClient) SetDialer(dialer adapter.DialerFunc) {
	g.conn.setServer(dialer)
}
