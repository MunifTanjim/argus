package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestServerWrapMatchesPrefix(t *testing.T) {
	s := NewServer()
	echo := func(name string) HandlerFunc {
		return func(context.Context, json.RawMessage) (any, error) { return name, nil }
	}
	s.Handle("bot.list", echo("list"))
	s.Handle("bot.show", echo("show"))
	s.Handle("ping", echo("ping"))
	s.Wrap("bot.", func(method string, h HandlerFunc) HandlerFunc {
		return func(ctx context.Context, p json.RawMessage) (any, error) {
			res, err := h(ctx, p)
			return method + ":" + res.(string), err
		}
	})
	for method, want := range map[string]string{"bot.list": "bot.list:list", "bot.show": "bot.show:show", "ping": "ping"} {
		got, err := s.DispatchFunc()(context.Background(), method, nil)
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %s", method, got, err, want)
		}
	}
}
