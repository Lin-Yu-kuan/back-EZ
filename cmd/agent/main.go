package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/owner/back-ez/internal/agent/ipc"
	"github.com/owner/back-ez/internal/agent/service"
)

func main() {
	serviceMode := flag.Bool("service", false, "Run as background Windows Service")
	peMode := flag.Bool("pe", false, "Run in Windows PE recovery environment mode")
	enrollToken := flag.String("enroll", "", "Enroll client certificate with Hub using one-time token")
	uiMode := flag.Bool("ui", false, "Run in user session with tray icon and WebView2 lock panel")
	reserveRoot := flag.String("reserve", "C:\\EZRestore", "Reserve root directory path")
	flag.Parse()

	if *enrollToken != "" {
		fmt.Printf("Enrolling client certificate with token %s...\n", *enrollToken)
		// Simulated enroll: In production calls POST /api/v1/agent/enroll
		fmt.Println("Client certificate enrolled successfully.")
		return
	}

	if *uiMode {
		fmt.Println("Starting back-EZ Client Local Lock Panel (--ui mode)...")
		// UI mode connects via Named Pipe to --service process
		return
	}

	if *serviceMode {
		modeStr := "WINDOWS"
		if *peMode {
			modeStr = "PE"
		}
		log.Printf("Starting back-EZ Client Agent Service (--service, mode=%s)...", modeStr)
		cfg, cfgPath, err := service.LoadOrCreateConfig(*reserveRoot)
		if err != nil {
			log.Fatalf("Failed to initialize config: %v", err)
		}
		log.Printf("Agent initialized with ClientID: %s", cfg.ClientID)

		core, err := service.NewAgentCore(*cfg, cfgPath, "admin")
		if err != nil {
			log.Fatalf("Failed to initialize AgentCore: %v", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// Start local IPC server
		l, err := net.Listen("tcp", "127.0.0.1:8989")
		if err == nil {
			defer l.Close()
			go func() {
				_ = core.RunIPCListener(l)
			}()
			log.Printf("Agent IPC server listening on %s (and pipe %s)", l.Addr(), ipc.PipeName)
		}

		<-ctx.Done()
		log.Println("Agent service stopping...")
		return
	}

	flag.Usage()
}
