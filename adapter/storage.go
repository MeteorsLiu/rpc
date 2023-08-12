package adapter

type Storage interface {
	Store(id string, info []byte) error
	ForEach(func(id string, info []byte) bool) error
	Get(id string) (info []byte, err error)
	Delete(id string) error
	Close()
}
