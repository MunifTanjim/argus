package api

import (
	"encoding/json"
	"net"
)

// Notification is a remote-initiated message (no response expected).
type Notification struct {
	Method string
	Params json.RawMessage
}

// Client is a JSON-RPC client over a stream connection. It is a thin consumer
// wrapper around a Peer that serves no inbound methods and surfaces
// notifications on a channel.
type Client struct {
	peer   *Peer
	events chan Notification
}

// Dial connects to a unix socket node.
func Dial(socket string) (*Client, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// NewClient wraps an established connection and starts its read loop.
func NewClient(conn net.Conn) *Client {
	c := &Client{events: make(chan Notification, 64)}
	c.peer = NewPeer(conn, PeerOptions{
		OnNotify: func(n Notification) {
			select {
			case c.events <- n:
			default: // drop if the consumer is slow
			}
		},
	})
	// Close events once the connection ends; safe since no OnNotify fires after Done.
	go func() {
		<-c.peer.Done()
		close(c.events)
	}()
	return c
}

// Events returns the channel of remote notifications. Closed when the connection
// ends.
func (c *Client) Events() <-chan Notification { return c.events }

// States satisfies the same client interface as ReconnectingClient. A plain Client
// never reconnects, so it returns a nil channel (which never fires).
func (c *Client) States() <-chan bool { return nil }

// Reconnect is a no-op: a plain Client does not reconnect.
func (c *Client) Reconnect() {}

// Close terminates the connection.
func (c *Client) Close() error { return c.peer.Close() }

// Call sends a request and unmarshals the result into out (which may be nil).
func (c *Client) Call(method string, params, out any) error {
	return c.peer.Call(method, params, out)
}
