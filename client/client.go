package client

import (
	"encoding/json"
	"io"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/MeteorsLiu/rpc/common/gorpc"
	"github.com/MeteorsLiu/rpc/common/utils"
	"github.com/MeteorsLiu/simpleMQ/queue"
	"github.com/MeteorsLiu/simpleMQ/worker"
)

type Options func(*Client)
type Middleware func(task queue.Task, serviceMethod string, args any, reply any)

type Job struct {
	// task id
	ID     string
	Method string
	Args   map[string]any
}

type Client struct {
	noretry     bool
	middlewares []Middleware
	nq          *worker.Worker
	mq          *worker.Worker
	rpc         adapter.Client
	storage     adapter.Storage
}

func WithStorage(storage adapter.Storage) Options {
	return func(c *Client) {
		c.storage = storage
	}
}

func WithWorker(w *worker.Worker) Options {
	return func(c *Client) {
		c.mq = w
	}
}

func WithMiddleware(m Middleware) Options {
	return func(c *Client) {
		c.middlewares = append(c.middlewares, m)
	}
}

func DisableRetry() Options {
	return func(c *Client) {
		c.noretry = true
	}
}

func NewClient(rpc adapter.Client, opts ...Options) *Client {
	c := &Client{
		nq: worker.NewWorker(0, 0, nil, false),
		// limit the worker number
		mq:  worker.NewWorker(10000, 100, queue.NewSimpleQueue(queue.WithSimpleQueueCap(10000)), true),
		rpc: rpc,
	}
	for _, o := range opts {
		o(c)
	}
	c.doRecoverJob()
	return c
}

func (c *Client) doMiddleware(task queue.Task, serviceMethod string, args any, reply any) {
	for _, m := range c.middlewares {
		m(task, serviceMethod, args, reply)
	}
}

func (c *Client) doRecoverJob() {
	if c.storage == nil {
		return
	}
	c.storage.ForEach(func(id string, info []byte) bool {
		var job Job
		json.Unmarshal(info, &job)
		if job.ID == "" {
			return true
		}
		c.Call(job.Method, job.Args, nil)
		return true
	})
}

func (c *Client) doSaveJob(task queue.Task, serviceMethod string, args any, reply any) {
	if c.storage == nil {
		return
	}
	job := &Job{
		ID:     task.ID(),
		Method: serviceMethod,
		Args:   utils.ToMap(args),
	}
	b, _ := json.Marshal(job)
	c.storage.Store(job.ID, b)
}

func (c *Client) newTask(serviceMethod string, args any, reply any) queue.Task {
	var task queue.Task
	if c.noretry {
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		}, queue.WithNoRetryFunc())
	} else {
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		})
	}
	c.doMiddleware(task, serviceMethod, args, reply)
	return task
}

func (c *Client) CallWithNoMQ(serviceMethod string, args any, reply any) error {
	task := c.newTask(serviceMethod, args, reply)

	if !c.noretry {
		c.nq.Publish(task)
		return nil
	}
	return task.Do()
}

func (c *Client) Call(serviceMethod string, args any, reply any) error {
	task := c.newTask(serviceMethod, args, reply)

	if !c.noretry {
		c.mq.Publish(task, func(ok bool, task queue.Task) {
			if !ok {
				c.doSaveJob(task, serviceMethod, args, reply)
			}
		})
		return nil
	}
	return task.Do()
}

func (c *Client) CallWithConn(conn io.ReadWriteCloser, serviceMethod string, args any, reply any) error {
	var task queue.Task
	if c.noretry {
		task = queue.NewTask(func() error {
			err := c.rpc.CallWithConn(conn, serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		}, queue.WithNoRetryFunc())
	} else {
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		})
	}
	c.doMiddleware(task, serviceMethod, args, reply)

	if !c.noretry {
		c.mq.Publish(task)
		return nil
	}
	return task.Do()
}

func (c *Client) Close() error {
	c.nq.Stop()
	c.mq.Stop()
	return nil
}
