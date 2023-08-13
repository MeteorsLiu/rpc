package client

import (
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/MeteorsLiu/rpc/common/gorpc"
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
func TestClient(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:9999")
	defer l.Close()
	srv := gorpc.NewGoRPCServer()
	srv.Register(&TestRPCStruct{})
	cli, _ := gorpc.NewGoRPCClient("127.0.0.1:9999")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		srv.Accept(l)
	}()
	// wait until server starts
	wg.Wait()
	c := NewClient(cli)
	var reply TestReply
	c.Call("TestRPCStruct.Hello", &TestArgs{"bbbb", 0}, &reply)
	t.Log(reply)

	c.CallOnce("TestRPCStruct.Hello", &TestArgs{"ccc", 1}, &reply)
	t.Log(reply)
	c.CallAsync("TestRPCStruct.Hello", &TestArgs{"ddd", 2}, &reply)
	t.Log(reply)

	c.Close()
}
