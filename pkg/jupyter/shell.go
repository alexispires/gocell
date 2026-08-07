package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/go-zeromq/zmq4"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/session"
	"github.com/alexispires/gocell/pkg/workspace"
)

// Server manages the main Jupyter kernel server.
type Server struct {
	conn           *ConnectionInfo
	sess           *session.Session
	executionCount uint64
}

// NewServer creates a new gocell server.
func NewServer(conn *ConnectionInfo, wsMgr *workspace.Manager) (*Server, error) {
	sess, err := session.New(wsMgr)
	if err != nil {
		return nil, err
	}

	return &Server{
		conn: conn,
		sess: sess,
	}, nil
}

// Interrupt asks the cell currently running on the Shell loop (if any) to stop at its next
// cooperative check. Safe to call from a different goroutine, e.g. the Control loop handling
// an interrupt_request, or a SIGINT handler.
func (s *Server) Interrupt() {
	s.sess.Interrupt()
}

// StartShellLoop listens for and handles messages on the ZMQ Shell channel.
func (s *Server) StartShellLoop(ctx context.Context, iopub *IOPubNotifier) error {
	addr := fmt.Sprintf("%s://%s:%d", s.conn.Transport, s.conn.IP, s.conn.ShellPort)
	socket := zmq4.NewRouter(ctx)

	if err := socket.Listen(addr); err != nil {
		return fmt.Errorf("failed to start Shell socket on %s: %w", addr, err)
	}
	defer func() { _ = socket.Close() }()

	key := []byte(s.conn.Key)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			zmsg, err := socket.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("[Shell] Receive error: %v", err)
				continue
			}

			msg, err := DecodeMessage(zmsg, key)
			if err != nil {
				log.Printf("[Shell] Message decode error: %v", err)
				continue
			}

			if err := iopub.SendStatus(msg.Header, "busy"); err != nil {
				log.Printf("[Shell] SendStatus(busy) error: %v", err)
			}

			switch msg.Header.MsgType {
			case "kernel_info_request":
				s.handleKernelInfoRequest(socket, msg, key)

			case "execute_request":
				s.handleExecuteRequest(socket, msg, key, iopub)

			case "is_complete_request":
				s.handleIsCompleteRequest(socket, msg, key)

			case "complete_request":
				s.handleCompleteRequest(socket, msg, key)

			default:
				log.Printf("[Shell] Ignored message type: %s", msg.Header.MsgType)
			}

			if err := iopub.SendStatus(msg.Header, "idle"); err != nil {
				log.Printf("[Shell] SendStatus(idle) error: %v", err)
			}
		}
	}
}

func (s *Server) handleKernelInfoRequest(socket zmq4.Socket, msg *Message, key []byte) {
	content, _ := json.Marshal(map[string]any{
		"protocol_version":       "5.3",
		"implementation":         "gocell",
		"implementation_version": "0.1.0",
		"language_info": map[string]any{
			"name":           "go",
			"version":        "1.22",
			"mimetype":       "text/x-gosrc",
			"file_extension": ".go",
		},
		"banner": "gocell - Dynamic Go Jupyter Kernel (Plugins & Shared Pointers)",
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("kernel_info_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      content,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	if err := socket.Send(zreply); err != nil {
		log.Printf("[Shell] kernel_info_reply send error: %v", err)
	}
}

func (s *Server) handleIsCompleteRequest(socket zmq4.Socket, msg *Message, key []byte) {
	var req struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(msg.Content, &req)

	status := "complete"
	if compiler.BraceDepth(req.Code) > 0 {
		status = "incomplete"
	}
	// indent is deliberately empty even when incomplete: gocell does no auto-indentation of
	// continuation lines by design (see gocell-repl), so there's nothing honest to suggest.
	content, _ := json.Marshal(map[string]string{
		"status": status,
		"indent": "",
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("is_complete_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      content,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	if err := socket.Send(zreply); err != nil {
		log.Printf("[Shell] is_complete_reply send error: %v", err)
	}
}

func (s *Server) handleCompleteRequest(socket zmq4.Socket, msg *Message, key []byte) {
	var req struct {
		Code      string `json:"code"`
		CursorPos int    `json:"cursor_pos"`
	}
	if err := json.Unmarshal(msg.Content, &req); err != nil {
		log.Printf("[Shell] complete_request unmarshal error: %v", err)
		return
	}

	matches, start, end := s.sess.Complete(req.Code, req.CursorPos)
	if matches == nil {
		matches = []string{}
	}

	content, _ := json.Marshal(map[string]any{
		"matches":      matches,
		"cursor_start": start,
		"cursor_end":   end,
		"metadata":     map[string]any{},
		"status":       "ok",
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("complete_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      content,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	if err := socket.Send(zreply); err != nil {
		log.Printf("[Shell] complete_reply send error: %v", err)
	}
}

func (s *Server) handleExecuteRequest(socket zmq4.Socket, msg *Message, key []byte, iopub *IOPubNotifier) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(msg.Content, &req); err != nil {
		log.Printf("[Shell] execute_request unmarshal error: %v", err)
		return
	}

	// A plain increment, not atomic.AddUint64: StartShellLoop is a single sequential loop
	// (this method is never called from more than one goroutine at a time), so there is no
	// concurrent access for atomic to guard against.
	s.executionCount++
	count := int(s.executionCount)
	if err := iopub.SendExecuteInput(msg.Header, req.Code, count); err != nil {
		log.Printf("[Shell] SendExecuteInput error: %v", err)
	}

	res, execErr := s.sess.Execute(req.Code)

	if res.Stdout != "" {
		if err := iopub.SendStream(msg.Header, "stdout", res.Stdout); err != nil {
			log.Printf("[Shell] SendStream(stdout) error: %v", err)
		}
	}
	if res.Stderr != "" {
		if err := iopub.SendStream(msg.Header, "stderr", res.Stderr); err != nil {
			log.Printf("[Shell] SendStream(stderr) error: %v", err)
		}
	}

	status := "ok"
	if execErr != nil {
		status = "error"
		log.Printf("[Shell Error] Cell execution failed: %v", execErr)
		if err := iopub.SendError(msg.Header, "ExecutionError", execErr.Error(), []string{execErr.Error()}); err != nil {
			log.Printf("[Shell] SendError error: %v", err)
		}
	} else if res.HasDisplay {
		if err := iopub.SendExecuteResult(msg.Header, count, res.DisplayText); err != nil {
			log.Printf("[Shell] SendExecuteResult error: %v", err)
		}
	}

	replyContent, _ := json.Marshal(map[string]any{
		"status":           status,
		"execution_count":  count,
		"user_expressions": map[string]any{},
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("execute_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      replyContent,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	if err := socket.Send(zreply); err != nil {
		log.Printf("[Shell] execute_reply send error: %v", err)
	}
}
