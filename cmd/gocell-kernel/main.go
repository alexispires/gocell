package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-zeromq/zmq4"

	"gocell/pkg/jupyter"
	"gocell/pkg/workspace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: gocell-kernel <connection_file>")
		os.Exit(1)
	}

	connPath := os.Args[1]
	conn, err := jupyter.ReadConnectionFile(connPath)
	if err != nil {
		log.Fatalf("Failed to read connection file: %v", err)
	}

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		log.Fatalf("Failed to initialize workspace: %v", err)
	}
	defer wsMgr.CleanUp()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT / SIGTERM system signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// 1. Start IOPub
	iopubAddr := fmt.Sprintf("%s://%s:%d", conn.Transport, conn.IP, conn.IOPubPort)
	iopubSocket := zmq4.NewPub(ctx)
	if err := iopubSocket.Listen(iopubAddr); err != nil {
		log.Fatalf("Failed to start IOPub socket on %s: %v", iopubAddr, err)
	}
	defer iopubSocket.Close()

	iopub := jupyter.NewIOPubNotifier(iopubSocket, []byte(conn.Key))

	// 2. Start Heartbeat
	if err := jupyter.StartHeartbeat(ctx, conn); err != nil {
		log.Fatalf("Failed to start Heartbeat: %v", err)
	}

	// 3. Start Control Loop
	if err := jupyter.StartControlLoop(ctx, conn, iopub, cancel); err != nil {
		log.Fatalf("Failed to start Control loop: %v", err)
	}

	// 4. Start Shell Loop
	server, err := jupyter.NewServer(conn, wsMgr)
	if err != nil {
		log.Fatalf("Failed to initialize gocell server: %v", err)
	}

	log.Printf("gocell Kernel started successfully. Listening on Shell channel %s:%d...", conn.IP, conn.ShellPort)

	if err := server.StartShellLoop(ctx, iopub); err != nil {
		log.Fatalf("Shell loop error: %v", err)
	}
}
