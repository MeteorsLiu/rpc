package redis

import (
	"context"
	"time"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/MeteorsLiu/rpc/storage/common"
	r "github.com/redis/go-redis/v9"
)

var (
	cb = context.Background()
)

type Options func(*r.Options)

type RedisClient struct {
	db *r.Client
}

func WithDB(n int) Options {
	return func(o *r.Options) {
		o.DB = n
	}
}

func WithAddr(addr string) Options {
	return func(o *r.Options) {
		o.Addr = addr
	}
}
func WithPassword(password string) Options {
	return func(o *r.Options) {
		o.Password = password
	}
}

func ping(c *r.Client) error {
	ctx, cancel := context.WithTimeout(cb, 10*time.Second)
	defer cancel()
	return c.Ping(ctx).Err()
}

func NewRedis(opts ...Options) (adapter.Storage, error) {
	rc := &r.Options{}
	for _, o := range opts {
		o(rc)
	}
	c := r.NewClient(rc)
	if err := ping(c); err != nil {
		return nil, err
	}
	return &RedisClient{c}, nil
}

func (r *RedisClient) Store(id string, info []byte) error {
	return r.db.HSet(cb, "tasks", id, string(info)).Err()
}
func (r *RedisClient) ForEach(f func(id string, info []byte) bool) error {
	tasks, err := r.db.HGetAll(cb, "tasks").Result()
	if err != nil {
		return err
	}
	for id, task := range tasks {
		if !f(id, []byte(task)) {
			return common.ErrIterStop
		}
		r.db.HDel(cb, "tasks", id)
	}
	return nil
}
func (r *RedisClient) Get(id string) (info []byte, err error) {
	info, err = r.db.HGet(cb, "tasks", id).Bytes()
	return
}

func (r *RedisClient) Delete(id string) error {
	return r.db.HDel(cb, "tasks", id).Err()
}

func (r *RedisClient) Close() {
	r.db.Close()
}
