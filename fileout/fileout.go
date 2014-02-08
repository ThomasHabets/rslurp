package fileout

import (
	"io"
	"os"
)

type FileOut interface {
	Create(fn string, size int64) (io.WriteCloser, error)
	Append(fn string, size int64) (io.WriteCloser, error)
	Close() error
	HasPartial() bool
}

type NormalFileOut struct{}

func (*NormalFileOut) HasPartial() bool {
	return true
}

func (*NormalFileOut) Create(fn string, size int64) (io.WriteCloser, error) {
	return os.Create(fn)
}

func (*NormalFileOut) Append(fn string, size int64) (io.WriteCloser, error) {
	return os.OpenFile(fn, os.O_APPEND|os.O_WRONLY, 0660)
}

func (*NormalFileOut) Close() error {
	return nil
}
