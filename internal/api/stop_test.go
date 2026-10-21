package api

import (
	"context"
	"testing"
	"time"
)

func TestHandleStopRepliesThenStops(t *testing.T) {
	old := StopDelay
	StopDelay = 20 * time.Millisecond
	t.Cleanup(func() { StopDelay = old })

	srv := NewServer()
	stopped := make(chan struct{})
	HandleStop(srv, func() { close(stopped) })

	sock := shortSocket(t)
	l, err := ListenUnix(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeContext(ctx, l)

	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(MethodServerStop, nil, nil); err != nil {
		t.Fatalf("server.stop = %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop was not called")
	}
}
