package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/owner/back-ez/internal/agent/service"
	"github.com/owner/back-ez/internal/agent/ui"
	"github.com/owner/back-ez/internal/workers/homesku"
	"github.com/owner/back-ez/internal/workers/vhdx"
)

func openBrowser(url string) {
	// Try Edge in app mode first for a clean window experience
	edgePath := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
	if _, err := os.Stat(edgePath); err == nil {
		cmd := exec.Command(edgePath, "--app="+url)
		if err := cmd.Start(); err == nil {
			return
		}
	}

	// Fallback to default browser
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}

func main() {
	reserveRoot := flag.String("reserve", "D:\\EZRestore", "Reserve root directory path")
	port := flag.Int("port", 28888, "Local GUI Web dashboard port")
	dryRun := flag.Bool("dryrun", false, "Simulate BCD/disk operations without modifying real disk")
	flag.Parse()

	log.Printf("啟動 back-EZ 本機秒級還原整合系統 (DryRun=%t)...", *dryRun)

	// 1. 初始化日誌記錄器
	logFile := filepath.Join(*reserveRoot, "logs", "operation.log")
	logger := service.NewOperationLogger(logFile)
	logger.Log("SYSTEM", "APP_START", "SUCCESS", fmt.Sprintf("back-EZ 控制台已啟動 (模式: DryRun=%t, 保留區: %s)", *dryRun, *reserveRoot))

	// 2. 初始化並載入 AgentCore
	cfg, cfgPath, err := service.LoadOrCreateConfig(*reserveRoot)
	if err != nil {
		logger.Log("SYSTEM", "CONFIG", "FAILED", fmt.Sprintf("無法載入 Agent 設定: %v", err))
		log.Fatalf("無法載入 Agent 設定: %v", err)
	}

	core, err := service.NewAgentCore(*cfg, cfgPath, "admin")
	if err != nil {
		logger.Log("SYSTEM", "CORE", "FAILED", fmt.Sprintf("無法初始化 Agent 核心: %v", err))
		log.Fatalf("無法初始化 Agent 核心: %v", err)
	}
	core.SetLogger(logger)

	// 3. 初始化底層 vhdx.Engine
	var exec vhdx.CommandExecutor
	if *dryRun {
		exec = vhdx.NewDryRunExecutor()
	} else {
		exec = &vhdx.RealExecutor{}
	}

	engineCfg := vhdx.Config{
		DryRun:           *dryRun,
		AllowDestructive: !*dryRun,
		ReserveRoot:      *reserveRoot,
		DataDir:          *reserveRoot,
		BCDEntryID:       "{default}",
	}
	engine, err := vhdx.NewEngine(engineCfg, exec)
	if err == nil {
		core.SetEngine(engine)
		logger.Log("SYSTEM", "VHDX_ENGINE", "SUCCESS", "底層 VHDX/BCD 引擎已成功就緒")
	} else {
		logger.Log("SYSTEM", "VHDX_ENGINE", "FAILED", fmt.Sprintf("初始化 VHDX 引擎失敗: %v", err))
	}

	// 4. 檢測 Windows 版本
	detector := homesku.NewDetector()
	edition, _ := detector.DetectEdition()
	isHome := detector.IsHomeEdition()

	log.Printf("偵測到 Windows 版本: %s (是否為家用版: %t)", edition, isHome)

	// 5. 設置輕量 Localhost REST API 與 GUI Web Server
	mux := http.NewServeMux()

	// 首頁：注入完整 GUI 控制台
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := ui.GenerateFullPanelHTML(isHome, string(edition))
		_, _ = w.Write([]byte(html))
	})

	// API: 列出還原點
	mux.HandleFunc("GET /api/restore-points", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		points, _ := core.ListRestorePoints()
		_ = json.NewEncoder(w).Encode(points)
	})

	// API: 操作日誌
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(logger.GetEntries())
	})

	// API: 新增還原點
	mux.HandleFunc("POST /api/restore-points/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "參數錯誤"})
			return
		}
		if err := core.CreateRestorePoint(body.Name, body.Description); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// API: 切換還原點
	mux.HandleFunc("POST /api/restore-points/switch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PointID string `json:"point_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "參數錯誤"})
			return
		}
		if err := core.SwitchRestorePoint(body.PointID); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// API: 覆蓋還原點
	mux.HandleFunc("POST /api/restore-points/overwrite", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PointID string `json:"point_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "參數錯誤"})
			return
		}
		if err := core.OverwriteRestorePoint(body.PointID); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// API: 刪除還原點
	mux.HandleFunc("POST /api/restore-points/delete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			PointID string `json:"point_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "參數錯誤"})
			return
		}
		if err := core.DeleteRestorePoint(body.PointID); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// API: 家用版一鍵解鎖 Pro
	mux.HandleFunc("POST /api/system/unlock-pro", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := detector.UnlockPro(); err != nil {
			logger.Log("SYSTEM", "UNLOCK_PRO", "FAILED", err.Error())
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		logger.Log("SYSTEM", "UNLOCK_PRO", "SUCCESS", "已成功觸發微軟官方 changepk.exe 提權程序")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// 綁定本地回環介面
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("無法監聽本地通訊埠 %s: %v", addr, err)
	}

	serverURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	log.Printf("本地控制台已啟動: %s", serverURL)

	// 啟動 HTTP 伺服器
	go func() {
		if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 伺服器錯誤: %v", err)
		}
	}()

	// 6. 自動彈出桌面應用視窗
	go openBrowser(serverURL)

	// 等待中斷訊號
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logger.Log("SYSTEM", "APP_STOP", "SUCCESS", "back-EZ 控制台已安全關閉")
	log.Println("正在安全關閉 back-EZ 控制台...")
}
