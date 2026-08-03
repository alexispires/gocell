package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/go-zeromq/zmq4"

	"gosk/pkg/session"
	"gosk/pkg/workspace"
)

// Server manages the main Jupyter kernel server.
type Server struct {
	conn           *ConnectionInfo
	sess           *session.Session
	executionCount uint64
}

// NewServer creates a new gosk server.
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

// StartShellLoop listens for and handles messages on the ZMQ Shell channel.
func (s *Server) StartShellLoop(ctx context.Context, iopub *IOPubNotifier) error {
	addr := fmt.Sprintf("%s://%s:%d", s.conn.Transport, s.conn.IP, s.conn.ShellPort)
	socket := zmq4.NewRouter(ctx)

	if err := socket.Listen(addr); err != nil {
		return fmt.Errorf("failed to start Shell socket on %s: %w", addr, err)
	}
	defer socket.Close()

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

			_ = iopub.SendStatus(msg.Header, "busy")

			switch msg.Header.MsgType {
			case "kernel_info_request":
				s.handleKernelInfoRequest(socket, msg, key)

			case "execute_request":
				s.handleExecuteRequest(socket, msg, key, iopub)

			case "is_complete_request":
				s.handleIsCompleteRequest(socket, msg, key)

			default:
				log.Printf("[Shell] Ignored message type: %s", msg.Header.MsgType)
			}

			_ = iopub.SendStatus(msg.Header, "idle")
		}
	}
}

func (s *Server) handleKernelInfoRequest(socket zmq4.Socket, msg *Message, key []byte) {
	content, _ := json.Marshal(map[string]any{
		"protocol_version":       "5.3",
		"implementation":         "gosk",
		"implementation_version": "0.1.0",
		"language_info": map[string]any{
			"name":           "go",
			"version":        "1.22",
			"mimetype":       "text/x-gosrc",
			"file_extension": ".go",
		},
		"banner": "gosk - Dynamic Go Jupyter Kernel (Plugins & Shared Pointers)",
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("kernel_info_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      content,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	_ = socket.Send(zreply)
}

func (s *Server) handleIsCompleteRequest(socket zmq4.Socket, msg *Message, key []byte) {
	content, _ := json.Marshal(map[string]string{
		"status": "complete",
	})

	replyMsg := &Message{
		Identities:   msg.Identities,
		Header:       NewHeader("is_complete_reply", msg.Header.Session),
		ParentHeader: msg.Header,
		Metadata:     make(map[string]any),
		Content:      content,
	}

	zreply, _ := EncodeMessage(replyMsg, key)
	_ = socket.Send(zreply)
}

func (s *Server) handleExecuteRequest(socket zmq4.Socket, msg *Message, key []byte, iopub *IOPubNotifier) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(msg.Content, &req); err != nil {
		log.Printf("[Shell] execute_request unmarshal error: %v", err)
		return
	}

	count := int(atomic.AddUint64(&s.executionCount, 1))
	_ = iopub.SendExecuteInput(msg.Header, req.Code, count)

	res, execErr := s.sess.Execute(req.Code)

	if res.Stdout != "" {
		_ = iopub.SendStream(msg.Header, "stdout", res.Stdout)
	}
	if res.Stderr != "" {
		_ = iopub.SendStream(msg.Header, "stderr", res.Stderr)
	}

	status := "ok"
	if execErr != nil {
		status = "error"
		log.Printf("[Shell Error] Cell execution failed: %v", execErr)
		_ = iopub.SendError(msg.Header, "ExecutionError", execErr.Error(), []string{execErr.Error()})
	} else if res.HasDisplay {
		_ = iopub.SendExecuteResult(msg.Header, count, res.DisplayText)
	}

	replyContent, _ := json.Marshal(map[string]any{
		"status":          status,
		"execution_count": count,
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
	_ = socket.Send(zreply)
}
