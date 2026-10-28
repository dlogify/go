// Package goid reads the current goroutine's id from the header of its stack
// trace ("goroutine 123 [running]:"). It is only used where Go offers no
// goroutine-local state: the BeforeSend recursion guard and stack headers.
package goid

import (
	"bytes"
	"runtime"
	"strconv"
)

func Get() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(b[:i]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
