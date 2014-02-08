package fileout

// Obviously not concurrency safe on the same TarOut.

import (
	"archive/tar"
	"fmt"
	"io"
)

type tarOutFile struct {
	w *TarOut
}

func (t *tarOutFile) Write(data []byte) (int, error) {
	return t.w.w.Write(data)
}
func (t *tarOutFile) Close() error {
	return t.w.w.Flush()
}

type TarOut struct {
	w *tar.Writer
}

func NewTarOut(w io.Writer) *TarOut {
	return &TarOut{
		w: tar.NewWriter(w),
	}
}

func (*TarOut) HasPartial() bool {
	return false
}
func (t *TarOut) Append(fn string, size int64) (io.WriteCloser, error) {
	return nil, fmt.Errorf("Tar output does not support resume.")
}

func (t *TarOut) Create(fn string, size int64) (io.WriteCloser, error) {
	if err := t.w.WriteHeader(&tar.Header{
		Name: fn,
		Size: size,
		// 	Mode       int64     // permission and mode bits
		// Uid        int       // user id of owner
		// Gid        int       // group id of owner
		// Size       int64     // length in bytes
		// ModTime    time.Time // modified time
		// Typeflag   byte      // type of header entry
		// Linkname   string    // target name of link
		// Uname      string    // user name of owner
		// Gname      string    // group name of owner
		// Devmajor   int64     // major number of character or block device
		// Devminor   int64     // minor number of character or block device
		// AccessTime time.Time // access time
		// ChangeTime time.Time // status change time
	}); err != nil {
		return nil, err
	}
	return &tarOutFile{
		w: t,
	}, nil
}

func (t *TarOut) Close() error {
	return t.w.Flush()
}
