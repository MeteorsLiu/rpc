package gorpc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"net/rpc/jsonrpc"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MeteorsLiu/rpc/adapter"
	"golang.org/x/crypto/ocsp"
)

var (
	ErrConnInit    = fmt.Errorf("connection is not initilized")
	ErrCertError   = fmt.Errorf("cert error")
	ErrConnect     = fmt.Errorf("fail to connect to the target address")
	ErrInitialized = fmt.Errorf("fail to initilize the connection pool")
	ErrNoServer    = fmt.Errorf("no rpc server")
)

func IsRPCServerError(err error) bool {
	_, ok := err.(rpc.ServerError)
	return ok
}

func IsCertError(err error) bool {
	return errors.Is(err, ErrCertError)
}

type RPCClientOption func(*GoRPCClient)
type PoolOptions func(*connPool)

func queryOCSP(url string, client, issuer *x509.Certificate) error {
	req, err := ocsp.CreateRequest(client, issuer, &ocsp.RequestOptions{
		Hash: crypto.SHA256,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(req))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/ocsp-request")
	httpReq.Header.Set("Accept", "application/ocsp-response")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrCertError
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	ocspResp, err := ocsp.ParseResponseForCert(b, client, issuer)
	if err != nil {
		return ErrCertError
	}

	if ocspResp.Status != ocsp.Good {
		return ErrCertError
	}
	return nil
}

func verifyPeerCertificate(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
	if len(verifiedChains) == 0 || len(verifiedChains[0]) < 2 {
		return nil
	}
	client := verifiedChains[0][0]
	issuer := verifiedChains[0][1]

	if len(client.OCSPServer) > 0 && client.OCSPServer[0] != "" {
		if err := queryOCSP(client.OCSPServer[0], client, issuer); err != nil {
			// ignore the case when we connect to the ocsp server fail
			if IsCertError(err) {
				return err
			}
		}
	}

	return nil
}

func defaultTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:            tls.VersionTLS13,
		VerifyPeerCertificate: verifyPeerCertificate,
	}
}

func WithCACert(cert []byte) RPCClientOption {
	return func(gr *GoRPCClient) {
		if gr.tls == nil {
			gr.tls = defaultTLSConfig()
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
			gr.tls = defaultTLSConfig()
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
		gr.conn = newConnPool(dialer)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func WithClientTLSConfig(c *tls.Config) RPCClientOption {
	return func(gr *GoRPCClient) {
		gr.tls = c
		gr.tls.VerifyPeerCertificate = verifyPeerCertificate
	}
}

func DefaultDialerFunc(address string) adapter.DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		return net.DialTimeout("tcp", address, 30*time.Second)
	}
}

func DefaultTLSDialerFunc(address string, c *tls.Config, onTLSFail func()) adapter.DialerFunc {
	return func() (io.ReadWriteCloser, error) {
		rwc, err := tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", address, c)
		if err != nil {
			log.Println("TLS Dial: ", address, err)
			if IsCertError(err) && onTLSFail != nil {
				onTLSFail()
			}
		}
		return rwc, err
	}
}

func WithTLSFail(f func()) RPCClientOption {
	return func(gr *GoRPCClient) {
		gr.onTLSFail = f
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
	// do safe work
	if cc == nil {
		return
	}
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

func newConnPool(c adapter.DialerFunc) *connPool {
	cp := &connPool{
		connWarper: c,
	}

	cp.close, cp.doClose = context.WithCancel(context.Background())
	cn := newConn(nil)
	cn.err = ErrConnInit
	// pool must be initialized,
	// however, the rpc server may not be ready right now,
	// so we set an error to indicate to reconnect in the future request.
	cp.conns = append(cp.conns, cn)

	return cp
}

func (c *connPool) dial() (io.ReadWriteCloser, error) {
	c.updateMu.RLock()
	dialerFunc := c.connWarper
	c.updateMu.RUnlock()
	return dialerFunc()
}

func (c *connPool) push(cn *conn) (id int) {
	c.resizeMu.Lock()
	c.conns = append(c.conns, cn)
	id = int(c.seq.Add(1))
	cn.id = id
	c.resizeMu.Unlock()
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
	if cn == nil || cn.err != nil {
		if cn != nil {
			if err := cn.Reconnect(c.dial, nil, func(cnn *conn) {
				cnn.Unlock()
			}); err == nil {
				cc = cn.c
				ok = true
				return
			}
		}
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
	onTLSFail func()
	conn      *connPool
	tls       *tls.Config
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
		cc.conn = newConnPool(DefaultDialerFunc(address))
	case cc.tls != nil && cc.conn == nil:
		cc.conn = newConnPool(DefaultTLSDialerFunc(address, cc.tls, cc.onTLSFail))
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
		if !IsRPCServerError(err) {
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
		g.conn.setServer(DefaultTLSDialerFunc(address, g.tls, g.onTLSFail))
	} else {
		g.conn.setServer(DefaultDialerFunc(address))
	}
	return nil
}

func (g *GoRPCClient) SetDialer(dialer adapter.DialerFunc) {
	g.conn.setServer(dialer)
}
