package gorpc

import (
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/MeteorsLiu/rpc/common/utils"
	"github.com/alphadose/haxmap"
)

type TestArgs struct {
	A  string
	ID int
}

type TestReply struct {
	ID  int
	Ret string
}

type TestRPCStruct struct {
}

func (t *TestRPCStruct) Hello(args *TestArgs, reply *TestReply) error {
	fmt.Println("recv", args.A, args.ID)
	reply.Ret = "B"
	reply.ID = args.ID
	return nil
}

func TestRPC(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:9999")
	defer l.Close()
	srv := NewGoRPCServer()
	srv.Register(&TestRPCStruct{})
	cli, _ := NewGoRPCClient("127.0.0.1:9999")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		srv.Accept(l)
	}()
	// wait until server starts
	wg.Wait()

	wg.Add(1000)

	for i := 0; i < 1000; i++ {
		go func(id int) {
			defer wg.Done()
			var reply TestReply
			// time sleep is to check the numbers of reused connections.
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			cli.Call("TestRPCStruct.Hello", &TestArgs{"ccccc", id}, &reply)
			if reply.ID != id {
				t.Errorf("rpc result error")
			}
		}(i)
	}
	wg.Wait()
	t.Log(cli.(*GoRPCClient).conn.seq.Load())
}

func BenchmarkHashMap(b *testing.B) {
	hx := haxmap.New[int, struct{}]()
	for i := 0; i < 1000; i++ {
		hx.Set(i, struct{}{})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = hx.Get(i % 1000)
	}
}

func BenchmarkSlice(b *testing.B) {
	s := make([]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		s[i] = struct{}{}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s[i%1000]
	}
}

func TestMTLSRPC(t *testing.T) {
	srvcert, err := tls.LoadX509KeyPair("certs/server.crt", "certs/server.key")
	if err != nil {
		t.Error(err)
		return
	}
	ca, err := os.ReadFile("certs/ca.crt")
	if err != nil {
		t.Error(err)
		return
	}
	clicert, err := tls.LoadX509KeyPair("certs/client.a.crt", "certs/client.a.key")
	if err != nil {
		t.Error(err)
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:9998")
	if err != nil {
		t.Error(err)
		return
	}
	defer l.Close()
	srv := NewGoRPCServer(WithClientCA(ca), WithServerCert(srvcert))
	srv.Register(&TestRPCStruct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		srv.Accept(l)
	}()
	// wait until server starts
	wg.Wait()

	cli, _ := NewGoRPCClient("localhost:9998", WithCACert(ca), WithClientCert(clicert))
	var reply TestReply
	if err := cli.Call("TestRPCStruct.Hello", &TestArgs{"ccccc", 0}, &reply); err != nil {
		t.Error(err)
	}
	if reply.ID != 0 {
		t.Errorf("rpc result error")
	}
}

func TestRPCMap(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:9999")
	defer l.Close()
	srv := NewGoRPCServer(WithServerMiddleware(func(methodName string, args any) error {
		t.Log("call: ", methodName, args)

		return nil
	}), WithServerFinalizer(func(err error, methodName string, args, reply any) {
		t.Log("call: ", methodName, args, reply, err)

	}))
	srv.Register(&TestRPCStruct{})
	cli, _ := NewGoRPCClient("127.0.0.1:9999")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		srv.Accept(l)
	}()
	// wait until server starts
	wg.Wait()
	tm := utils.ToMap(&TestArgs{"bbbb", 0})
	ret := map[string]any{}
	//var reply TestReply
	// time sleep is to check the numbers of reused connections.
	cli.Call("TestRPCStruct.Hello", tm, &ret)
	t.Log(ret)
}

func TestRPCMiddleware(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:9999")
	defer l.Close()
	srv := NewGoRPCServer(WithServerMiddleware(func(methodName string, args any) error {
		t.Log("call: ", methodName, args)

		return fmt.Errorf("middleware error")
	}), WithServerFinalizer(func(err error, methodName string, args, reply any) {
		t.Log("call: ", methodName, args, reply, err)

	}))
	srv.Register(&TestRPCStruct{})
	cli, _ := NewGoRPCClient("127.0.0.1:9999")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		srv.Accept(l)
	}()
	// wait until server starts
	wg.Wait()
	tm := utils.ToMap(&TestArgs{"bbbb", 0})
	ret := map[string]any{}
	//var reply TestReply
	// time sleep is to check the numbers of reused connections.
	err := cli.Call("TestRPCStruct.Hello", tm, &ret)
	t.Log(ret, err)
}
