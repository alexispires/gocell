package jupyter

import (
	"context"
	"fmt"
	"log"

	"github.com/go-zeromq/zmq4"
)

// StartHeartbeat starts the ZMQ Heartbeat (Echo) loop on the specified port.
func StartHeartbeat(ctx context.Context, conn *ConnectionInfo) error {
	addr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.HbPort)
	socket := zmq4.NewRep(ctx)

	if err := socket.Listen(addr); err != nil {
		return fmt.Errorf("failed to start Heartbeat socket on %s: %w", addr, err)
	}

	go func() {
		defer socket.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := socket.Recv()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[Heartbeat] Receive error: %v", err)
					continue
				}
				// Immediate echo reply
				_ = socket.Send(msg)
			}
		}
	}()

	return nil
}
