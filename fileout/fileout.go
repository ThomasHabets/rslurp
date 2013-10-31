package fileout

import (
	"io"
	"os"
)

type FileOut interface {
	Create(fn string, size int64) (io.WriteCloser, error)
	Close() error
}

type NormalFileOut struct{}

func (*NormalFileOut) Create(fn string, size int64) (io.WriteCloser, error) {
	return os.Create(fn)
}

func (*NormalFileOut) Close() error {
	return nil
}
