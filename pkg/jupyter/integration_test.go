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

	"github.com/alexispires/gocell/pkg/jupyter"
	"github.com/alexispires/gocell/pkg/workspace"
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

// zmqTestKernel is a running gocell kernel plus fake shell and IOPub-subscriber clients,
// wired the same way TestJupyterZMQIntegration sets one up -- factored out so a second test
// can also inspect IOPub content without duplicating the ~60 lines of socket plumbing.
type zmqTestKernel struct {
	clientShell   zmq4.Socket
	clientControl zmq4.Socket
	iopubSub      zmq4.Socket
	key           []byte
}

func newZMQTestKernel(t *testing.T) *zmqTestKernel {
	t.Helper()

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

	tmpDir := t.TempDir()
	connPath := filepath.Join(tmpDir, "connection.json")
	connData, _ := json.Marshal(conn)
	_ = os.WriteFile(connPath, connData, 0644)

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	t.Cleanup(func() { _ = wsMgr.CleanUp() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	iopubAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.IOPubPort)
	iopubSocket := zmq4.NewPub(ctx)
	if err := iopubSocket.Listen(iopubAddr); err != nil {
		t.Fatalf("Failed to listen on IOPub: %v", err)
	}
	t.Cleanup(func() { _ = iopubSocket.Close() })

	iopub := jupyter.NewIOPubNotifier(iopubSocket, []byte(conn.Key))

	if err := jupyter.StartHeartbeat(ctx, conn); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	server, err := jupyter.NewServer(conn, wsMgr)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	if err := jupyter.StartControlLoop(ctx, conn, iopub, cancel, server.Interrupt); err != nil {
		t.Fatalf("Control failed: %v", err)
	}
	go func() { _ = server.StartShellLoop(ctx, iopub) }()

	shellAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.ShellPort)
	clientShell := zmq4.NewDealer(ctx)
	if err := clientShell.Dial(shellAddr); err != nil {
		t.Fatalf("Failed to connect Shell client: %v", err)
	}
	t.Cleanup(func() { _ = clientShell.Close() })

	controlAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.ControlPort)
	clientControl := zmq4.NewDealer(ctx)
	if err := clientControl.Dial(controlAddr); err != nil {
		t.Fatalf("Failed to connect Control client: %v", err)
	}
	t.Cleanup(func() { _ = clientControl.Close() })

	// A second fake client subscribed to IOPub, the channel that carries stream/error/
	// execute_result content -- the shell reply alone only carries the execute_reply status.
	iopubSub := zmq4.NewSub(ctx)
	if err := iopubSub.Dial(iopubAddr); err != nil {
		t.Fatalf("Failed to connect IOPub subscriber: %v", err)
	}
	if err := iopubSub.SetOption(zmq4.OptionSubscribe, ""); err != nil {
		t.Fatalf("Failed to subscribe to IOPub: %v", err)
	}
	t.Cleanup(func() { _ = iopubSub.Close() })

	// Give the listeners and the subscription time to establish before anything is sent --
	// a PUB socket drops messages published before a SUB has finished subscribing.
	time.Sleep(200 * time.Millisecond)

	return &zmqTestKernel{
		clientShell:   clientShell,
		clientControl: clientControl,
		iopubSub:      iopubSub,
		key:           []byte(conn.Key),
	}
}

// execute sends an execute_request and returns the decoded execute_reply.
func (k *zmqTestKernel) execute(t *testing.T, code string) *jupyter.Message {
	t.Helper()
	k.sendExecute(t, code)
	return k.recvExecuteReply(t)
}

// sendExecute sends an execute_request without waiting for the reply -- for a cell expected
// to hang until interrupted, execute (send-then-immediately-block-on-Recv) would itself block
// forever.
func (k *zmqTestKernel) sendExecute(t *testing.T, code string) {
	t.Helper()

	execReqContent, _ := json.Marshal(map[string]string{"code": code})
	req := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("execute_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      execReqContent,
	}

	zmsg, _ := jupyter.EncodeMessage(req, k.key)
	if err := k.clientShell.Send(zmsg); err != nil {
		t.Fatalf("Failed to send execute_request: %v", err)
	}
}

// recvExecuteReply blocks for the execute_reply to a previously sent execute_request.
func (k *zmqTestKernel) recvExecuteReply(t *testing.T) *jupyter.Message {
	t.Helper()

	replyZMsg, err := k.clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive execute_reply: %v", err)
	}
	replyMsg, err := jupyter.DecodeMessage(replyZMsg, k.key)
	if err != nil {
		t.Fatalf("Failed to decode execute_reply: %v", err)
	}
	return replyMsg
}

// complete sends a complete_request and returns the decoded complete_reply.
func (k *zmqTestKernel) complete(t *testing.T, code string, cursorPos int) *jupyter.Message {
	t.Helper()

	reqContent, _ := json.Marshal(map[string]any{"code": code, "cursor_pos": cursorPos})
	req := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("complete_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      reqContent,
	}

	zmsg, _ := jupyter.EncodeMessage(req, k.key)
	if err := k.clientShell.Send(zmsg); err != nil {
		t.Fatalf("Failed to send complete_request: %v", err)
	}

	replyZMsg, err := k.clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive complete_reply: %v", err)
	}
	replyMsg, err := jupyter.DecodeMessage(replyZMsg, k.key)
	if err != nil {
		t.Fatalf("Failed to decode complete_reply: %v", err)
	}
	return replyMsg
}

// isComplete sends an is_complete_request and returns the decoded is_complete_reply.
func (k *zmqTestKernel) isComplete(t *testing.T, code string) *jupyter.Message {
	t.Helper()

	reqContent, _ := json.Marshal(map[string]string{"code": code})
	req := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("is_complete_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      reqContent,
	}

	zmsg, _ := jupyter.EncodeMessage(req, k.key)
	if err := k.clientShell.Send(zmsg); err != nil {
		t.Fatalf("Failed to send is_complete_request: %v", err)
	}

	replyZMsg, err := k.clientShell.Recv()
	if err != nil {
		t.Fatalf("Failed to receive is_complete_reply: %v", err)
	}
	replyMsg, err := jupyter.DecodeMessage(replyZMsg, k.key)
	if err != nil {
		t.Fatalf("Failed to decode is_complete_reply: %v", err)
	}
	return replyMsg
}

// interrupt sends an interrupt_request on the Control channel and returns the decoded
// interrupt_reply -- the actual delivery path a Jupyter client's "Interrupt" button uses.
func (k *zmqTestKernel) interrupt(t *testing.T) *jupyter.Message {
	t.Helper()

	req := &jupyter.Message{
		Identities:   [][]byte{[]byte("client-dealer")},
		Header:       jupyter.NewHeader("interrupt_request", "session-1"),
		ParentHeader: jupyter.Header{},
		Metadata:     make(map[string]any),
		Content:      json.RawMessage("{}"),
	}

	zmsg, _ := jupyter.EncodeMessage(req, k.key)
	if err := k.clientControl.Send(zmsg); err != nil {
		t.Fatalf("Failed to send interrupt_request: %v", err)
	}

	replyZMsg, err := k.clientControl.Recv()
	if err != nil {
		t.Fatalf("Failed to receive interrupt_reply: %v", err)
	}
	replyMsg, err := jupyter.DecodeMessage(replyZMsg, k.key)
	if err != nil {
		t.Fatalf("Failed to decode interrupt_reply: %v", err)
	}
	return replyMsg
}

// recvIOPubUntil reads IOPub messages (with a short per-message timeout) until one matches
// msgType, and fails the test if none arrives within the overall deadline.
func (k *zmqTestKernel) recvIOPubUntil(t *testing.T, msgType string) *jupyter.Message {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		zmsg, err := k.iopubSub.Recv()
		if err != nil {
			t.Fatalf("IOPub Recv failed: %v", err)
		}
		msg, err := jupyter.DecodeMessage(zmsg, k.key)
		if err != nil {
			t.Fatalf("Failed to decode IOPub message: %v", err)
		}
		if msg.Header.MsgType == msgType {
			return msg
		}
	}
	t.Fatalf("Timed out waiting for an IOPub message of type %q", msgType)
	return nil
}

func TestJupyterZMQIntegration(t *testing.T) {
	k := newZMQTestKernel(t)
	clientShell := k.clientShell
	key := k.key

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
		LanguageInfo   struct {
			Version string `json:"version"`
		} `json:"language_info"`
	}
	_ = json.Unmarshal(replyMsg.Content, &infoContent)
	if infoContent.Implementation != "gocell" {
		t.Fatalf("Expected implementation 'gocell', got '%s'", infoContent.Implementation)
	}
	// Regression guard: language_info.version used to be hardcoded to a stale "1.22", drifting
	// from go.mod's own declared version (see pkg/compiler.Builder.GoVersion). It must at least
	// report a real, non-empty version now -- not the literal old hardcoded string.
	if infoContent.LanguageInfo.Version == "" || infoContent.LanguageInfo.Version == "1.22" {
		t.Fatalf("Expected a real, non-hardcoded Go version, got %q", infoContent.LanguageInfo.Version)
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

// TestJupyterZMQPublishesStreamMessages verifies that a cell writing to stdout gets that
// output published on IOPub as a "stream" message with the correct name and text -- not just
// that the execute_reply itself reports status "ok". TestJupyterZMQIntegration never inspects
// IOPub content at all, so this is a layer the existing suite left uncovered.
func TestJupyterZMQPublishesStreamMessages(t *testing.T) {
	k := newZMQTestKernel(t)

	reply := k.execute(t, `import "fmt"; fmt.Println("hello from stdout")`)
	var status struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(reply.Content, &status)
	if status.Status != "ok" {
		t.Fatalf("Expected execute_reply status 'ok', got %q", status.Status)
	}

	streamMsg := k.recvIOPubUntil(t, "stream")
	var stream struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(streamMsg.Content, &stream); err != nil {
		t.Fatalf("Failed to decode stream content: %v", err)
	}
	if stream.Name != "stdout" {
		t.Fatalf("Expected stream name 'stdout', got %q", stream.Name)
	}
	if stream.Text != "hello from stdout\n" {
		t.Fatalf("Expected stream text 'hello from stdout\\n', got %q", stream.Text)
	}
}

// TestJupyterZMQPublishesErrorOnPanic verifies that a panicking cell publishes an "error"
// message on IOPub (with ename/evalue), in addition to the execute_reply reporting status
// "error" -- a Jupyter client (JupyterLab, VS Code) renders the cell's error output from this
// IOPub message, not from the execute_reply, so an execute_reply-only check can't catch a
// regression here.
func TestJupyterZMQPublishesErrorOnPanic(t *testing.T) {
	k := newZMQTestKernel(t)

	reply := k.execute(t, `panic("boom")`)
	var status struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(reply.Content, &status)
	if status.Status != "error" {
		t.Fatalf("Expected execute_reply status 'error', got %q", status.Status)
	}

	errMsg := k.recvIOPubUntil(t, "error")
	var errContent struct {
		Ename  string `json:"ename"`
		Evalue string `json:"evalue"`
	}
	if err := json.Unmarshal(errMsg.Content, &errContent); err != nil {
		t.Fatalf("Failed to decode error content: %v", err)
	}
	if errContent.Ename == "" {
		t.Fatalf("Expected a non-empty ename on the published error message")
	}
}

// TestJupyterZMQCompleteRequest verifies a real complete_request/complete_reply round trip:
// this is the message pair a client (JupyterLab, VS Code) sends on Tab, entirely unhandled
// before this feature -- shell.go's dispatch switch only knew kernel_info_request,
// execute_request, and is_complete_request (a different message, unrelated to suggestions).
func TestJupyterZMQCompleteRequest(t *testing.T) {
	k := newZMQTestKernel(t)

	reply := k.execute(t, `countdown := 10`)
	var execStatus struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(reply.Content, &execStatus)
	if execStatus.Status != "ok" {
		t.Fatalf("Expected execute_reply status 'ok', got %q", execStatus.Status)
	}

	code := "coun"
	completeReply := k.complete(t, code, len(code))
	if completeReply.Header.MsgType != "complete_reply" {
		t.Fatalf("Expected message type 'complete_reply', got %q", completeReply.Header.MsgType)
	}

	var content struct {
		Matches     []string `json:"matches"`
		CursorStart int      `json:"cursor_start"`
		CursorEnd   int      `json:"cursor_end"`
		Status      string   `json:"status"`
	}
	if err := json.Unmarshal(completeReply.Content, &content); err != nil {
		t.Fatalf("Failed to decode complete_reply content: %v", err)
	}
	if content.Status != "ok" {
		t.Fatalf("Expected complete_reply status 'ok', got %q", content.Status)
	}
	if content.CursorStart != 0 || content.CursorEnd != len(code) {
		t.Fatalf("Expected cursor range [0, %d), got [%d, %d)", len(code), content.CursorStart, content.CursorEnd)
	}
	found := false
	for _, m := range content.Matches {
		if m == "countdown" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Expected 'countdown' among complete_reply matches, got %v", content.Matches)
	}
}

// TestJupyterZMQCompleteRequestMemberAccess exercises the go/types-backed `foo.` path over
// the real ZMQ round trip, not just Session.Complete directly.
func TestJupyterZMQCompleteRequestMemberAccess(t *testing.T) {
	k := newZMQTestKernel(t)

	reply := k.execute(t, `type Point struct{ X, Y int }
p := &Point{X: 1, Y: 2}`)
	var execStatus struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(reply.Content, &execStatus)
	if execStatus.Status != "ok" {
		t.Fatalf("Expected execute_reply status 'ok', got %q", execStatus.Status)
	}

	code := "p."
	completeReply := k.complete(t, code, len(code))

	var content struct {
		Matches []string `json:"matches"`
		Status  string   `json:"status"`
	}
	if err := json.Unmarshal(completeReply.Content, &content); err != nil {
		t.Fatalf("Failed to decode complete_reply content: %v", err)
	}
	if content.Status != "ok" {
		t.Fatalf("Expected complete_reply status 'ok', got %q", content.Status)
	}
	for _, want := range []string{"X", "Y"} {
		found := false
		for _, m := range content.Matches {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Expected %q among complete_reply matches, got %v", want, content.Matches)
		}
	}
}

// TestJupyterZMQIsCompleteRequest verifies is_complete_request now actually reflects brace
// balance instead of always answering "complete" -- and specifically that a `{` inside a
// string literal (the exact bug this session fixed) doesn't fool it into reporting
// "incomplete" for already-finished code.
func TestJupyterZMQIsCompleteRequest(t *testing.T) {
	k := newZMQTestKernel(t)

	cases := []struct {
		name string
		code string
		want string
	}{
		{"balanced", "if true {\n\tx := 1\n}", "complete"},
		{"still open", "if true {\n\tx := 1", "incomplete"},
		{"brace inside a string", `fmt.Println("Result: {")`, "complete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := k.isComplete(t, c.code)
			var content struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(reply.Content, &content); err != nil {
				t.Fatalf("Failed to decode is_complete_reply content: %v", err)
			}
			if content.Status != c.want {
				t.Fatalf("Expected status %q for %q, got %q", c.want, c.code, content.Status)
			}
		})
	}
}

// TestJupyterZMQInterruptStopsAHangingCell is the real end-to-end proof for the delivery path
// the backlog identified as non-functional: interrupt_request on Control was previously a
// no-op that replied without touching the stuck cell. Sends a hanging execute_request on
// Shell (which blocks that loop entirely), then interrupt_request on Control -- handled
// concurrently, since StartControlLoop runs its own goroutine independent of Shell's -- and
// confirms the execute_reply eventually arrives with status "error" instead of hanging
// forever.
func TestJupyterZMQInterruptStopsAHangingCell(t *testing.T) {
	k := newZMQTestKernel(t)

	k.sendExecute(t, "for {\n}")
	time.Sleep(300 * time.Millisecond) // let the loop actually start spinning on Shell

	interruptReply := k.interrupt(t)
	if interruptReply.Header.MsgType != "interrupt_reply" {
		t.Fatalf("Expected message type 'interrupt_reply', got %q", interruptReply.Header.MsgType)
	}

	execReply := k.recvExecuteReply(t)
	var status struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(execReply.Content, &status)
	if status.Status != "error" {
		t.Fatalf("Expected execute_reply status 'error' after interrupt, got %q", status.Status)
	}
}
