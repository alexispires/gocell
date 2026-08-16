package jupyter

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/alexispires/gocell/pkg/runtime"
	"github.com/go-zeromq/zmq4"
)

// IOPubNotifier manages publishing messages on the ZMQ IOPub channel.
type IOPubNotifier struct {
	mu     sync.Mutex
	socket zmq4.Socket
	key    []byte
}

// NewIOPubNotifier creates the IOPub notifier.
func NewIOPubNotifier(socket zmq4.Socket, key []byte) *IOPubNotifier {
	return &IOPubNotifier{
		socket: socket,
		key:    key,
	}
}

// Publish sends a generic message on the IOPub channel.
func (p *IOPubNotifier) Publish(parent Header, msgType string, content any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	rawContent, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to encode IOPub content: %w", err)
	}

	msg := &Message{
		Identities:   [][]byte{[]byte(msgType)},
		Header:       NewHeader(msgType, parent.Session),
		ParentHeader: parent,
		Metadata:     make(map[string]any),
		Content:      rawContent,
	}

	zmsg, err := EncodeMessage(msg, p.key)
	if err != nil {
		return err
	}

	return p.socket.Send(zmsg)
}

// SendStatus publishes the kernel status ('busy' or 'idle').
func (p *IOPubNotifier) SendStatus(parent Header, executionState string) error {
	return p.Publish(parent, "status", map[string]string{
		"execution_state": executionState,
	})
}

// SendStream publishes text data on stdout or stderr.
func (p *IOPubNotifier) SendStream(parent Header, name, text string) error {
	if text == "" {
		return nil
	}
	return p.Publish(parent, "stream", map[string]string{
		"name": name,
		"text": text,
	})
}

// SendExecuteResult publishes the value of a cell's last expression (equivalent to the
// "Out[n]" displayed by other Jupyter kernels).
func (p *IOPubNotifier) SendExecuteResult(parent Header, executionCount int, out runtime.Output) error {
	return p.Publish(parent, "execute_result", map[string]any{
		"execution_count": executionCount,
		"data":            bundleOrEmpty(out.Data),
		"metadata":        metaOrEmpty(out.Meta),
	})
}

// SendDisplayData publishes something a cell showed explicitly. Same content as execute_result
// minus execution_count, which is the only thing separating the two message types.
func (p *IOPubNotifier) SendDisplayData(parent Header, out runtime.Output) error {
	return p.Publish(parent, "display_data", map[string]any{
		"data":     bundleOrEmpty(out.Data),
		"metadata": metaOrEmpty(out.Meta),
	})
}

// Both fields are required by the protocol and must serialize as {} rather than null.
func bundleOrEmpty(b runtime.Bundle) map[string]any {
	if b == nil {
		return map[string]any{}
	}
	return b
}

func metaOrEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// SendExecuteInput confirms to the client which code is currently being executed.
func (p *IOPubNotifier) SendExecuteInput(parent Header, code string, executionCount int) error {
	return p.Publish(parent, "execute_input", map[string]any{
		"code":            code,
		"execution_count": executionCount,
	})
}

// SendError publishes an execution error on IOPub.
func (p *IOPubNotifier) SendError(parent Header, ename, evalue string, traceback []string) error {
	return p.Publish(parent, "error", map[string]any{
		"ename":     ename,
		"evalue":    evalue,
		"traceback": traceback,
	})
}

// Close closes the IOPub socket.
func (p *IOPubNotifier) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.socket.Close()
}
