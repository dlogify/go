package goid

import "testing"

func TestGet(t *testing.T) {
	main := Get()
	other := make(chan int64)
	go func() { other <- Get() }()
	if id := <-other; main <= 0 || id <= 0 || id == main {
		t.Fatalf("ids %d %d", main, id)
	}
}
