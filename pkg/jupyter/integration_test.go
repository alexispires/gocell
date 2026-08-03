package jupyter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-zeromq/zmq4"

	"gosk/pkg/jupyter"
	"gosk/pkg/workspace"
)

// helper to reserve 5 free TCP ports
func getFreePorts(count int) ([]int, error) {
	var ports []int
	var listeners []net.Listener

	for i := 0; i < count; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, lis := range listeners {
				_ = lis.Close()
			}
			return nil, err
		}
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
		listeners = append(listeners, l)
	}

	for _, lis := range listeners {
		_ = lis.Close()
	}

	return ports, nil
}

func TestJupyterZMQIntegration(t *testing.T) {
	ports, err := getFreePorts(5)
	if err != nil {
		t.Fatalf("Failed to reserve ports: %v", err)
	}

	conn := &jupyter.ConnectionInfo{
		ControlPort:     ports[0],
		ShellPort:       ports[1],
		StdinPort:       ports[2],
		HbPort:          ports[3],
		IOPubPort:       ports[4],
		Transport:       "tcp",
		SignatureScheme: "hmac-sha256",
		IP:              "127.0.0.1",
		Key:             "test-secret-key-12345",
	}

	// Write a temporary connection file
	tmpDir := t.TempDir()
	connPath := filepath.Join(tmpDir, "connection.json")
	connData, _ := json.Marshal(conn)
	_ = os.WriteFile(connPath, connData, 0644)

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer wsMgr.CleanUp()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start the gosk Kernel server
	iopubAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.IOPubPort)
	iopubSocket := zmq4.NewPub(ctx)
	if err := iopubSocket.Listen(iopubAddr); err != nil {
		t.Fatalf("Failed to listen on IOPub: %v", err)
	}
	defer iopubSocket.Close()

	iopub := jupyter.NewIOPubNotifier(iopubSocket, []byte(conn.Key))

	if err := jupyter.StartHeartbeat(ctx, conn); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	if err := jupyter.StartControlLoop(ctx, conn, iopub, cancel); err != nil {
		t.Fatalf("Control failed: %v", err)
	}

	server, err := jupyter.NewServer(conn, wsMgr)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	go func() {
		_ = server.StartShellLoop(ctx, iopub)
	}()

	// Wait briefly for the listeners to initialize
	time.Sleep(100 * time.Millisecond)

	// 2. Fake ZMQ client simulating a Jupyter Client (VS Code / JupyterLab)
	shellAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.ShellPort)
	clientShell := zmq4.NewDealer(ctx)
	if err := clientShell.Dial(shellAddr); err != nil {
		t.Fatalf("Failed to connect Shell client: %v", err)
	}
	defer clientShell.Close()

	key := []byte(conn.Key)

	// --- Step 1: kernel_info_request ---
	reqKernelInfo := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("kernel_info_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      json.RawMessage("{}"),
	}

	zmsgKernelInfo, _ := jupyter.EncodeMessage(reqKernelInfo, key)
	if err := clientShell.Send(zmsgKernelInfo); err != nil {
		t.Fatalf("Failed to send kernel_info_request: %v", err)
	}

	replyZMsg, err := clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive kernel_info_reply: %v", err)
	}

	replyMsg, err := jupyter.DecodeMessage(replyZMsg, key)
	if err != nil {
		t.Fatalf("Failed to decode reply: %v", err)
	}

	if replyMsg.Header.MsgType != "kernel_info_reply" {
		t.Fatalf("Expected type 'kernel_info_reply', got '%s'", replyMsg.Header.MsgType)
	}

	var infoContent struct {
		Implementation string `json:"implementation"`
	}
	_ = json.Unmarshal(replyMsg.Content, &infoContent)
	if infoContent.Implementation != "gosk" {
		t.Fatalf("Expected implementation 'gosk', got '%s'", infoContent.Implementation)
	}

	// --- Step 2: execute_request (Cell 1: x := 100) ---
	execReqContent, _ := json.Marshal(map[string]string{
		"code": `x := 100`,
	})
	reqExec1 := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("execute_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      execReqContent,
	}

	zmsgExec1, _ := jupyter.EncodeMessage(reqExec1, key)
	if err := clientShell.Send(zmsgExec1); err != nil {
		t.Fatalf("Failed to send execute_request 1: %v", err)
	}

	replyExecZMsg1, err := clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive execute_reply 1: %v", err)
	}

	replyExecMsg1, _ := jupyter.DecodeMessage(replyExecZMsg1, key)
	if replyExecMsg1.Header.MsgType != "execute_reply" {
		t.Fatalf("Expected type 'execute_reply', got '%s'", replyExecMsg1.Header.MsgType)
	}

	var statusReply1 struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(replyExecMsg1.Content, &statusReply1)
	if statusReply1.Status != "ok" {
		t.Fatalf("Expected execution status 'ok', got '%s'", statusReply1.Status)
	}

	// --- Step 3: execute_request (Cell 2: fmt.Println(x + 50)) ---
	execReqContent2, _ := json.Marshal(map[string]string{
		"code": `fmt.Println("Result =", x + 50)`,
	})
	reqExec2 := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("execute_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      execReqContent2,
	}

	zmsgExec2, _ := jupyter.EncodeMessage(reqExec2, key)
	if err := clientShell.Send(zmsgExec2); err != nil {
		t.Fatalf("Failed to send execute_request 2: %v", err)
	}

	replyExecZMsg2, err := clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive execute_reply 2: %v", err)
	}

	replyExecMsg2, _ := jupyter.DecodeMessage(replyExecZMsg2, key)
	var statusReply2 struct {
		Status    string   `json:"status"`
		Traceback []string `json:"traceback"`
	}
	_ = json.Unmarshal(replyExecMsg2.Content, &statusReply2)
	if statusReply2.Status != "ok" {
		t.Fatalf("Expected execution 2 status 'ok', got 'error'. Traceback: %v", statusReply2.Traceback)
	}
}
