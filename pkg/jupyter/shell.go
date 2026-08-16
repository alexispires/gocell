package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/go-zeromq/zmq4"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/runtime"
	"github.com/alexispires/gocell/pkg/session"
	"github.com/alexispires/gocell/pkg/workspace"
)

// Server manages the main Jupyter kernel server.
type Server struct {
	conn           *ConnectionInfo
	sess           *session.Session
	executionCount uint64
	stdin          *StdinRequester

	// parent is the request being served, needed to address IOPub messages a cell publishes
	// while it runs. Read from cell goroutines, so it is guarded.
	parentMu sync.RWMutex
	parent   Header
}

// AttachIOPub routes a cell's display.Show straight to the frontend as it is called, rather than
// letting it queue until the cell ends. Nothing can update in place without this: every frame of a
// progress bar would arrive after the loop that drew it had already finished.
func (s *Server) AttachIOPub(iopub *IOPubNotifier) {
	s.sess.SetDisplayHook(func(out runtime.Output, displayID string, update bool) {
		s.parentMu.RLock()
		parent := s.parent
		s.parentMu.RUnlock()
		if err := iopub.SendDisplayData(parent, out, displayID, update); err != nil {
			log.Printf("[Shell] SendDisplayData error: %v", err)
		}
	})
}

// AttachStdin wires the Stdin channel to the session, so display.Input in a cell becomes an
// input_request to the frontend. Without it a cell asking for input gets an error instead of a
// prompt, which is the right outcome when there is no channel to ask on.
func (s *Server) AttachStdin(r *StdinRequester) {
	s.stdin = r
	s.sess.SetInputFunc(r.Request)
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

			case "inspect_request":
				s.handleInspectRequest(socket, msg, key)

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
			"version":        s.sess.GoVersion(),
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

// handleInspectRequest answers Shift-Tab. found=false is a normal answer, not an error: the
// frontend then shows its own help instead of an empty panel.
func (s *Server) handleInspectRequest(socket zmq4.Socket, msg *Message, key []byte) {
	var req struct {
		Code      string `json:"code"`
		CursorPos int    `json:"cursor_pos"`
	}
	if err := json.Unmarshal(msg.Content, &req); err != nil {
		log.Printf("[Shell] inspect_request unmarshal error: %v", err)
		return
	}

	text, found := s.sess.Inspect(req.Code, req.CursorPos)

	content := map[string]any{
		"status":   "ok",
		"found":    found,
		"data":     map[string]any{},
		"metadata": map[string]any{},
	}
	if found {
		// text/plain only: go doc's output is already laid out in columns, and wrapping it in
		// HTML would destroy the alignment it relies on.
		content["data"] = map[string]any{"text/plain": text}
	}

	replyContent, _ := json.Marshal(content)
	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("inspect_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      replyContent,
	}
	zreply, _ := EncodeMessage(replyMsg, key)
	if err := socket.Send(zreply); err != nil {
		log.Printf("[Shell] inspect_reply send error: %v", err)
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

	// Any input_request this cell raises has to be addressed to the client that sent this
	// execute_request, and parented to it.
	s.stdin.SetParent(msg)
	s.parentMu.Lock()
	s.parent = msg.Header
	s.parentMu.Unlock()

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
	} else {
		// Explicit display.Show output first, in call order, then the cell's own last
		// expression -- the same order Jupyter shows them in.
		// Anything still queued ran without a live hook; normally empty.
		for _, out := range res.Displays {
			if err := iopub.SendDisplayData(msg.Header, out, "", false); err != nil {
				log.Printf("[Shell] SendDisplayData error: %v", err)
			}
		}
		if res.HasResult {
			if err := iopub.SendExecuteResult(msg.Header, count, res.Result); err != nil {
				log.Printf("[Shell] SendExecuteResult error: %v", err)
			}
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
