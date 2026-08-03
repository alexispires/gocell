package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/go-zeromq/zmq4"
)

// StartControlLoop starts the priority listener on the ZMQ Control channel.
func StartControlLoop(ctx context.Context, conn *ConnectionInfo, iopub *IOPubNotifier, cancelFunc context.CancelFunc) error {
	addr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.ControlPort)
	socket := zmq4.NewRouter(ctx)

	if err := socket.Listen(addr); err != nil {
		return fmt.Errorf("failed to start Control socket on %s: %w", addr, err)
	}

	key := []byte(conn.Key)

	go func() {
		defer socket.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				zmsg, err := socket.Recv()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[Control] Receive error: %v", err)
					continue
				}

				msg, err := DecodeMessage(zmsg, key)
				if err != nil {
					log.Printf("[Control] Message decode error: %v", err)
					continue
				}

				_ = iopub.SendStatus(msg.Header, "busy")

				switch msg.Header.MsgType {
				case "shutdown_request":
					var req map[string]any
					_ = json.Unmarshal(msg.Content, &req)

					replyContent, _ := json.Marshal(map[string]any{
						"restart": req["restart"],
					})

					replyMsg := &Message{
						Identities:   msg.Identities,
						Header:       NewHeader("shutdown_reply", msg.Header.Session),
						ParentHeader: msg.Header,
						Metadata:     make(map[string]any),
						Content:      replyContent,
					}

					zreply, _ := EncodeMessage(replyMsg, key)
					_ = socket.Send(zreply)
					_ = iopub.SendStatus(msg.Header, "idle")

					cancelFunc()
					return

				case "interrupt_request":
					replyMsg := &Message{
						Identities:   msg.Identities,
						Header:       NewHeader("interrupt_reply", msg.Header.Session),
						ParentHeader: msg.Header,
						Metadata:     make(map[string]any),
						Content:      json.RawMessage("{}"),
					}

					zreply, _ := EncodeMessage(replyMsg, key)
					_ = socket.Send(zreply)
					_ = iopub.SendStatus(msg.Header, "idle")
				}
			}
		}
	}()

	return nil
}
