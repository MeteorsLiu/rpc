package client

import (
	"encoding/json"
	"io"
	"sync/atomic"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/MeteorsLiu/rpc/common/gorpc"
	"github.com/MeteorsLiu/rpc/common/utils"
	"github.com/MeteorsLiu/simpleMQ/queue"
	"github.com/MeteorsLiu/simpleMQ/worker"
)

type Options func(*Client)
type Middleware func(task queue.Task, serviceMethod string, args any, reply any)
type NeedSave func(bool, *Client, queue.Task) bool
type RunMethod int

const (
	SYNC RunMethod = iota
	ASYNC
	ONCE
	ASYNC_ONCE
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
	needSave    NeedSave
	nq          *worker.Worker
	mq          *worker.Worker
	rpc         adapter.Client
	// export to custom need save function
	Storage adapter.Storage
	// export to custom need save function
	Deleted atomic.Bool
}

func DefaultSave() NeedSave {
	return func(ok bool, c *Client, task queue.Task) bool {
		return (!ok || task.IsReachLimits()) && !c.Deleted.Load()
	}
}

func WithStorage(storage adapter.Storage) Options {
	return func(c *Client) {
		c.Storage = storage
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

func WithSaveCondition(f NeedSave) Options {
	return func(c *Client) {
		c.needSave = f
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
		block:    true,
		needSave: DefaultSave(),
		nq:       worker.NewWorker(0, 0, nil, true),
		// limit the worker number
		mq:  worker.NewWorker(10000, 1, queue.NewSimpleQueue(queue.WithSimpleQueueCap(10000)), true),
		rpc: rpc,
	}
	for _, o := range opts {
		o(c)
	}
	c.DoRecoverJob()
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

// export to user control
func (c *Client) DoRecoverJob() {
	if c.Storage == nil {
		return
	}
	c.Storage.ForEach(func(id string, info []byte) bool {
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
		case ASYNC_ONCE:
			c.CallAsyncOnce(job.Method, job.Args, nil)
		}
		return true
	})
}

func (c *Client) doSaveJob(task queue.Task, runMethod RunMethod, serviceMethod string, args any) {
	if c.Storage == nil {
		return
	}
	job := &Job{
		ID:        task.ID(),
		RunMethod: runMethod,
		Method:    serviceMethod,
		Args:      utils.ToMap(args),
	}
	b, _ := json.Marshal(job)
	c.Storage.Store(job.ID, b)

}

func (c *Client) newTask(
	serviceMethod string,
	args, reply any,
	needResult bool,
	opts ...queue.TaskOptions,
) queue.Task {
	var task queue.Task
	if c.noretry {
		opts = append(opts, queue.WithNoRetryFunc())
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				task.SetTaskError(err)
				return nil
			}
			return err
		}, opts...)
	} else {
		task = queue.NewTask(func() error {
			err := c.rpc.Call(serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				task.SetTaskError(err)
				return nil
			}
			return err
		}, opts...)
	}
	c.doMiddleware(task, serviceMethod, args, reply)
	if needResult && reply != nil {
		// must be first
		task.OnDone(func(_ bool, task queue.Task) {
			task.TaskContext().Store("reply", reply)
		})
	}
	task.OnDone(c.finalizers...)
	return task
}

func (c *Client) CallAsync(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply, true)
	task.OnDone(func(ok bool, task queue.Task) {
		if c.needSave(ok, c, task) {
			c.doSaveJob(task, ASYNC, serviceMethod, args)
		}
	})
	c.mq.Publish(task, finalizer...)
	return nil
}

func (c *Client) Call(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply, !c.block)
	task.OnDone(func(ok bool, task queue.Task) {
		if c.needSave(ok, c, task) {
			c.doSaveJob(task, c.runMethod(), serviceMethod, args)
		}
	})

	if !c.block {
		c.mq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)
}

func (c *Client) CallOnce(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply, !c.block, queue.WithNoRetryFunc())

	if !c.block {
		c.nq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)

}

func (c *Client) CallAsyncOnce(serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	task := c.newTask(serviceMethod, args, reply, true, queue.WithNoRetryFunc())
	c.nq.Publish(task, finalizer...)
	return nil
}

func (c *Client) Save(runMethod RunMethod, serviceMethod string, args any) queue.Finalizer {
	return func(ok bool, task queue.Task) {
		if c.needSave(ok, c, task) {
			c.doSaveJob(task, runMethod, serviceMethod, args)
		}
	}
}

func (c *Client) CallWithConn(conn io.ReadWriteCloser, serviceMethod string, args any, reply any, finalizer ...queue.Finalizer) error {
	var task queue.Task
	if c.noretry {
		task = queue.NewTask(func() error {
			err := c.rpc.CallWithConn(conn, serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				task.SetTaskError(err)
				return nil
			}
			return err
		}, queue.WithNoRetryFunc())
	} else {
		task = queue.NewTask(func() error {
			err := c.rpc.CallWithConn(conn, serviceMethod, args, reply)
			if gorpc.IsRPCServerError(err) {
				task.SetTaskError(err)
				return nil
			}
			return err
		})
	}
	c.doMiddleware(task, serviceMethod, args, reply)

	if reply != nil {
		task.OnDone(func(_ bool, task queue.Task) {
			task.TaskContext().Store("reply", reply)
		})
	}
	task.OnDone(c.finalizers...)
	if !c.block {
		c.mq.Publish(task, finalizer...)
		return nil
	}
	return c.nq.PublishSync(task, finalizer...)
}

func (c *Client) Close(isDeleted ...bool) error {
	if len(isDeleted) > 0 && isDeleted[0] {
		c.Deleted.Store(true)
	}
	c.nq.Stop()
	c.mq.Stop()
	return nil
}
