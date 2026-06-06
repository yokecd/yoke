package wasi

import "io/fs"

type ReadonlyFS struct {
	internal fs.FS
}

func (fs ReadonlyFS) Open(name string) (fs.File, error) {
	file, err := fs.internal.Open(name)
	if err != nil {
		return nil, err
	}
	return ReadonlyFile{file}, nil
}

var _ fs.File = ReadonlyFile{}

type ReadonlyFile struct {
	file fs.File
}

// Close implements [fs.File].
func (ro ReadonlyFile) Close() error {
	return ro.file.Close()
}

// Read implements [fs.File].
func (ro ReadonlyFile) Read(buffer []byte) (int, error) {
	return ro.file.Read(buffer)
}

// Stat implements [fs.File].
func (ro ReadonlyFile) Stat() (fs.FileInfo, error) {
	return ro.file.Stat()
}
