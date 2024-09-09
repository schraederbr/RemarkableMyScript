package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akeil/rmtool/internal/logging"
)

// A MessageHandler can be registered with the notifications client to receive
// incoming messages.
type MessageHandler func(Message)

// Notifications is the client for the notification service.
//
// It connects to the websocket service, parses messages from JSON
// and forwards them to a registered message handler.
type Notifications struct {
	url    string
	token  string
	conn   *websocket.Conn
	connMx sync.Mutex
	done   chan struct{}
	exit   chan struct{}
	hdl    MessageHandler
	hdlMx  sync.Mutex
}

// NewNotifications sets up a new notifications client.
func newNotifications(url, token string) *Notifications {
	// TODO: automatically refresh the token when it's expired
	return &Notifications{
		url:   url,
		token: token,
		done:  make(chan struct{}),
		exit:  make(chan struct{}),
	}
}

// Connect creates a new websocket connection to the notification service.
//
// After a connection is made, the notifications client starts to receive
// messages and dispatches them to the MessageHandler registered via OnMessage.
//
// Calling Connect while the client is already connected leads to a reconnect.
func (n *Notifications) Connect() error {
	n.connMx.Lock()
	defer n.connMx.Unlock()

	if n.isConnected() {
		n.Disconnect()
		// TODO: ideally, we would block until the connection is actually closed
	}
	n.conn = nil

	logging.Info("Connect to notification service at %q (using token: %v)\n", n.url, n.token != "")

	h := http.Header{}
	h.Set("Authorization", "Bearer "+n.token)
	conn, res, err := websocket.DefaultDialer.Dial(n.url, h)
	if err != nil {
		return fmt.Errorf("websocket connection failed with status %v, error %v", res.StatusCode, err)
	}

	n.conn = conn
	n.done = make(chan struct{})
	n.exit = make(chan struct{})

	go n.loop()
	go n.read()

	return nil
}

// isConnected checks whether we have an active connection to the notification
// service.
func (n *Notifications) isConnected() bool {
	return n.conn != nil
}

// Disconnect closes the connection with the notification server.
// Calling Disconnect while the client is already disconnected has no effect.
func (n *Notifications) Disconnect() {
	close(n.exit)
}

// onDisconnected is called internally after the connection has been closed.
func (n *Notifications) onDisconnected() {
	logging.Info("Notifications disconnected")
	n.connMx.Lock()
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
	}
	n.connMx.Unlock()

	// TODO: client code should be able to register a handler for this.
}

// loop is the "empty" write loop.
// since we never write anything, this is only used to send a close message.
// ...and maybe for keep alive messges?
func (n *Notifications) loop() {
	defer n.onDisconnected()

	for {
		select {
		case <-n.done:
			return
		case <-n.exit:
			// close the connection by sending a close message
			close := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			err := n.conn.WriteMessage(websocket.CloseMessage, close)
			if err != nil {
				logging.Debug("Websocket, write close: %v", err)
				return
			}
			// wait for server to close the connection (or timeout)
			select {
			case <-n.done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}

// read is the receive-loop for our websocket connection.
// It reads incoming messages an passes them to the internal message handler.
func (n *Notifications) read() {
	defer close(n.done)
	for {
		_, data, err := n.conn.ReadMessage()
		if err != nil {
			logging.Debug("Websocket read error: %v", err)
			// assume: server closed connection
			return
		}
		n.handleMessage(data)
	}
}

// handleMessage is called for each incoming message that is successfully received.
func (n *Notifications) handleMessage(data []byte) {
	n.hdlMx.Lock()
	handler := n.hdl
	n.hdlMx.Unlock()

	// early exit if there is nobody to receive the message
	if handler == nil {
		return
	}

	// parse content...
	var w msgWrapper
	dec := json.NewDecoder(bytes.NewReader(data))
	err := dec.Decode(&w)
	if err != nil {
		logging.Warning("Error decoding notification message: %v", err)
		logging.Debug(string(data))
	}

	// ...and dispatch
	go handler(w.toMessage())
}

// OnMessage registers a handler function for received messages.
//
// Setting a handler removes the current one; setting the handler to `nil`
// is allowed to remove the current handler.
//
// The handler function will be called in a separate goroutine for each message.
func (n *Notifications) OnMessage(f MessageHandler) {
	n.hdlMx.Lock()
	n.hdl = f
	n.hdlMx.Unlock()
}
