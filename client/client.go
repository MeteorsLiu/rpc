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

type RunMethod int

const (
	SYNC RunMethod = iota
	ASYNC
	ONCE
)

type Job struct {
	// task id
	ID        string
	RunMethod RunMethod
	Method    string
	Args      map[string]any
}

type Client struct {
	block       bool
	noretry     bool
	finalizers  []queue.Finalizer
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

func EnableNonBlocking() Options {
	return func(c *Client) {
		c.block = false
	}
}

func WithFinalizer(f queue.Finalizer) Options {
	return func(c *Client) {
		c.finalizers = append(c.finalizers, f)
	}
}

func NewClient(rpc adapter.Client, opts ...Options) *Client {
	c := &Client{
		block: true,
		nq:    worker.NewWorker(0, 0, nil, true),
		// limit the worker number
		mq:  worker.NewWorker(10000, 1, queue.NewSimpleQueue(queue.WithSimpleQueueCap(10000)), true),
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

func (c *Client) runMethod() RunMethod {
	if c.block {
		return SYNC
	}
	return ASYNC
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
		switch job.RunMethod {
		case SYNC:
			c.Call(job.Method, job.Args, nil)
		case ASYNC:
			c.CallAsync(job.Method, job.Args, nil)
		case ONCE:
			c.CallOnce(job.Method, job.Args, nil)
		}
		return true
	})
}

func (c *Client) doSaveJob(task queue.Task, runMethod RunMethod, serviceMethod string, args any, reply any) {
	if c.storage == nil {
		return
	}
	job := &Job{
		ID:        task.ID(),
		RunMethod: runMethod,
		Method:    serviceMethod,
		Args:      utils.ToMap(args),
	}
	b, _ := json.Marshal(job)
	c.storage.Store(job.ID, b)
}

func (c *Client) newTask(serviceMethod string, args any, reply any, opts ...queue.TaskOptions) queue.Task {
	var task queue.Task
	if c.noretry {
		opts = append(opts, queue.WithNoRetryFunc())
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		}, opts...)
	} else {
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				return nil
			}
			return err
		}, opts...)
	}
	c.doMiddleware(task, serviceMethod, args, reply)
	task.OnDone(c.finalizers...)
	return task
}

func (c *Client) CallAsync(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply)
	task.OnDone(func(ok bool, task queue.Task) {
		if !ok {
			c.doSaveJob(task, ASYNC, serviceMethod, args, reply)
		}
	})

	c.mq.Publish(task, finalizer...)
	return nil
}

func (c *Client) Call(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply)
	task.OnDone(func(ok bool, task queue.Task) {
		if !ok {
			c.doSaveJob(task, c.runMethod(), serviceMethod, args, reply)
		}
	})

	if !c.block {
		c.mq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)
}

func (c *Client) CallOnce(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply, queue.WithNoRetryFunc())

	if !c.block {
		c.nq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)

}

func (c *Client) CallWithConn(conn io.ReadWriteCloser, serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
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
	task.OnDone(c.finalizers...)
	if !c.block {
		c.mq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)
}

func (c *Client) Close() error {
	c.nq.Stop()
	c.mq.Stop()
	return nil
}
