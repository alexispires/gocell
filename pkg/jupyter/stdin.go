package jupyter

import (
	"sync"

	"github.com/go-zeromq/zmq4"
)

// StdinHandler manages user input requests on the ZMQ Stdin channel.
type StdinHandler struct {
	mu     sync.Mutex
	socket zmq4.Socket
	key    []byte
}

// NewStdinHandler creates a StdinHandler.
func NewStdinHandler(socket zmq4.Socket, key []byte) *StdinHandler {
	return &StdinHandler{
		socket: socket,
		key:    key,
	}
}

// Close closes the stdin socket.
func (s *StdinHandler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.socket.Close()
}
