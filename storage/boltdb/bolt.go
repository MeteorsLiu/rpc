package boltdb

import (
	"fmt"
	"time"

	"github.com/MeteorsLiu/rpc/adapter"
	"github.com/MeteorsLiu/rpc/storage/common"
	"github.com/boltdb/bolt"
)

type Bolt struct {
	db *bolt.DB
}

var (
	ErrBucketsNotExists = fmt.Errorf("bucket tasks doesn't exist")
	ErrItemNotExists    = fmt.Errorf("item doesn't exist")
)

func NewBoltDB(saveTo ...string) (adapter.Storage, error) {
	fileName := "saved.db"
	if len(saveTo) > 0 && saveTo[0] != "" {
		fileName = saveTo[0]
	}
	db, err := bolt.Open(fileName, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	db.Update(func(tx *bolt.Tx) error {
		tx.CreateBucketIfNotExists([]byte("tasks"))
		return nil
	})
	return &Bolt{db}, nil
}

func (b *Bolt) Store(id string, info []byte) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bs := tx.Bucket([]byte("tasks"))
		if bs == nil {
			return ErrBucketsNotExists
		}
		return bs.Put([]byte(id), info)
	})
}

func (b *Bolt) ForEach(f func(id string, info []byte) bool) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bs := tx.Bucket([]byte("tasks"))
		if bs == nil {
			return ErrBucketsNotExists
		}
		return bs.ForEach(func(k, v []byte) error {
			if !f(string(k), v) {
				return common.ErrIterStop
			}
			bs.Delete(k)
			return nil
		})
	})
}

func (b *Bolt) Get(id string) (info []byte, err error) {
	err = b.db.View(func(tx *bolt.Tx) error {
		bs := tx.Bucket([]byte("tasks"))
		if bs == nil {
			return ErrBucketsNotExists
		}
		c := bs.Get([]byte(id))
		if c == nil {
			return ErrItemNotExists
		}
		info = c
		return nil
	})

	return
}

func (b *Bolt) Delete(id string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bs := tx.Bucket([]byte("tasks"))
		if bs == nil {
			return ErrBucketsNotExists
		}
		return bs.Delete([]byte(id))
	})
}

func (b *Bolt) Close() {
	b.db.Close()
}
