package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/go-zeromq/zmq4"
)

// StdinRequester owns the Stdin channel, the one channel that runs backwards: the kernel asks and
// the frontend answers. It is how a cell reads from the user -- Jupyter has no terminal to attach a
// running cell to, so a prompt has to travel as a message.
//
// The round trip is synchronous by design. It is driven from the Shell loop, inside the cell's own
// Execute, which is exactly where the cell is blocked waiting for the value.
type StdinRequester struct {
	socket zmq4.Socket
	key    []byte

	// mu serialises whole request/reply exchanges. Two cells never overlap, but a cell's own
	// goroutine could call in while the main statement is mid-exchange, and the replies would be
	// indistinguishable on the wire.
	mu sync.Mutex

	// parent identifies the client to answer to, and threads the reply back to the cell that
	// asked. ZMQ ROUTER needs the identity frames of the original execute_request; without them
	// the input_request is delivered nowhere and the cell hangs on a reply that never comes.
	parentMu   sync.RWMutex
	parent     Header
	identities [][]byte
}

// NewStdinRequester binds the Stdin socket. It is a ROUTER like Shell: the kernel is the server,
// and messages must carry the client identity they are addressed to.
func NewStdinRequester(ctx context.Context, conn *ConnectionInfo) (*StdinRequester, error) {
	addr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.StdinPort)
	socket := zmq4.NewRouter(ctx)
	if err := socket.Listen(addr); err != nil {
		return nil, fmt.Errorf("failed to start Stdin socket on %s: %w", addr, err)
	}
	return &StdinRequester{socket: socket, key: []byte(conn.Key)}, nil
}

// SetParent records which request the kernel is currently serving. The Shell loop calls this before
// running a cell, so any input_request that cell triggers is addressed to the right client and
// parented to the right execute_request.
func (r *StdinRequester) SetParent(msg *Message) {
	if r == nil {
		return
	}
	r.parentMu.Lock()
	defer r.parentMu.Unlock()
	r.parent, r.identities = msg.Header, msg.Identities
}

// Request prompts the frontend and blocks until it replies.
//
// password suppresses echo on the client side; the value still arrives in clear, so it protects a
// shoulder, not a network.
func (r *StdinRequester) Request(prompt string, password bool) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no stdin channel: this kernel was started without one")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.parentMu.RLock()
	parent, identities := r.parent, r.identities
	r.parentMu.RUnlock()

	if len(identities) == 0 {
		return "", fmt.Errorf("no client to prompt: nothing is currently executing")
	}

	content, err := json.Marshal(map[string]any{"prompt": prompt, "password": password})
	if err != nil {
		return "", err
	}

	req := &Message{
		Identities:   identities,
		Header:       NewHeader("input_request", parent.Session),
		ParentHeader: parent,
		Metadata:     make(map[string]any),
		Content:      content,
	}
	zreq, err := EncodeMessage(req, r.key)
	if err != nil {
		return "", err
	}
	if err := r.socket.Send(zreq); err != nil {
		return "", fmt.Errorf("failed to send input_request: %w", err)
	}

	// No timeout: the user may take as long as they like to answer, and an interrupt is the way
	// out -- the same contract every other Jupyter kernel offers at a prompt.
	zreply, err := r.socket.Recv()
	if err != nil {
		return "", fmt.Errorf("failed to receive input_reply: %w", err)
	}
	reply, err := DecodeMessage(zreply, r.key)
	if err != nil {
		return "", err
	}
	if reply.Header.MsgType != "input_reply" {
		return "", fmt.Errorf("expected input_reply on the Stdin channel, got %q", reply.Header.MsgType)
	}

	var body struct {
		Value  string `json:"value"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(reply.Content, &body); err != nil {
		return "", fmt.Errorf("failed to decode input_reply: %w", err)
	}
	// A frontend that cannot prompt -- nbconvert, a headless run -- answers with an error status
	// rather than a value. Surfacing that beats returning an empty string the cell would use.
	if body.Status == "error" {
		return "", fmt.Errorf("the frontend declined to prompt for input")
	}
	return body.Value, nil
}

// Close releases the Stdin socket.
func (r *StdinRequester) Close() error {
	if r == nil {
		return nil
	}
	return r.socket.Close()
}
