# AI 開發守則與架構約束 (`AGENT.md`)

> [!IMPORTANT]
> 本文件為 AI Agent 在進行本專案開發時的最高行為準則。每次撰寫或修改程式碼前，必須對照 [`SPEC.md`](./SPEC.md) 與 [`TASKS.md`](./TASKS.md)，嚴禁偏離契約或自行發明未定義的欄位。

---

## 1. 角色定位與開發哲學

1. **規格優先（Spec-First）**：所有 JSON 欄位名稱、狀態機枚舉值、API 路徑與 WebSocket 事件名稱，100% 以 `SPEC.md` 為準，禁止自行改名（例如將 `ip_mode` 改為 `ipType`）。
2. **薄核心 ＋ 獨立 Worker（Thin Core + Workers）**：
   - 拒絕過度設計（No Over-engineering）：**嚴禁使用 Go 動態 `plugin` 套件或複雜的反射（Reflection）依賴注入框架**。
   - 採用標準 Go Package 進行編譯期模組解耦，保持單一 `.exe` 執行檔的高效與零依賴特性。
3. **增量可驗證（Incremental & Testable）**：每完成 `TASKS.md` 中的一個子項目，必須確保程式碼可編譯（`go build` / `npm run build`）且通過對應單元測試。

---

## 2. 技術棧與環境標準 (Tech Stack)

| 層級 | 選定技術 | 規範與限制 |
| :--- | :--- | :--- |
| **中控端後端 (Hub)** | Go 1.22+ | 使用標準庫 `net/http`、Gorilla/Coder WebSocket、SQLite (`modernc.org/sqlite` 無 CGO 純 Go 版，確保跨平台單一執行檔編譯)。 |
| **中控端前端 (Web UI)** | React + TypeScript + Vite + Tailwind CSS | 嚴格開啟 TypeScript `strict: true`，所有 API 型別定義集中於 `src/types/spec.ts`。 |
| **客戶端代理 (Agent)** | Go 1.22+ (Windows Service) | 負責本機狀態回報、密碼驗證、呼叫 `diskpart`/`bcdedit`、執行網路設定快照與復原。 |
| **群播與映像引擎** | 外部 CLI 封裝 (`udpcast`, `wimlib`/`partclone`, `diskpart`) | 透過 Go `os/exec` 封裝，支援 `DryRun`（模擬執行模式），方便在開發機上無損測試。 |

---

## 3. 專案標準目錄結構

```text
back-EZ/                        # 專案根目錄（Go module 路徑待決策）
├── SPEC.md                     # 系統技術規格白皮書
├── AGENT.md                    # AI 開發守則 (本文件)
├── TASKS.md                    # 任務分解與進度追蹤
├── cmd/
│   ├── hub/                    # 中控端主程式入口 (main.go)
│   └── agent/                  # 客戶端主程式入口 (main.go)
├── internal/
│   ├── protocol/               # 與 SPEC.md 1:1 對應的 Go Struct 定義 (共享契約)
│   ├── hub/                    # 中控端核心：HTTP API、WebSocket Hub、SQLite 儲存層
│   ├── agent/                  # 客戶端核心：心跳連線、密碼驗證、任務接收器
│   └── workers/                # 獨立功能模組 (Workers)
│       ├── wol/                # Wake-on-LAN 魔術封包發送器
│       ├── vhdx/               # VHDX 差分硬碟與 BCD 開機管理封裝
│       ├── pxe/                # PXE / TFTP / ProxyDHCP 引導（尚無對應 Task，見 SPEC §1.2 D2）
│       ├── netpreserve/        # 派送前 IP/Hostname 快照與派送後離線/在線還原
│       └── multicast/          # udp-sender / udp-receiver 進程控制與進度解析
└── web/                        # React + Tailwind CSS 中控台前端與本機鎖定面板
```

**Worker 命名對照（唯一合法名稱）**

| 概念名稱（＝Go package） | 目錄 | 對應 Task |
| :--- | :--- | :--- |
| `wol` | `internal/workers/wol` | 3.1 |
| `vhdx` | `internal/workers/vhdx` | 3.3 |
| `pxe` | `internal/workers/pxe` | **（尚無，見 SPEC §1.2 D2）** |
| `multicast` | `internal/workers/multicast` | 4.2 |
| `netpreserve`（`capture` / `restore`） | `internal/workers/netpreserve` | 4.1、4.3 |

- 舊稱 `wol_worker` / `vhdx_worker` / `pxe_worker` / `multicast_worker` / `net_preserve_worker` 一律廢止，不得再出現在程式碼、註解、文件或 JSON 欄位中。
- 術語固定：**中控端**＝Go Hub 本體（含內嵌靜態資源）；**中控台**＝其 Web UI；**Agent**＝客戶端 Windows 服務；**母機／子機**＝派送中的 Sender／Receiver。禁止用「中控台」指稱後端。

---

## 4. 關鍵領域開發紅線（嚴格遵守）

### 4.1 磁碟與開機指令防護（Safety Sandbox）
- `vhdx` 與 `multicast` 模組在呼叫 `diskpart`、`bcdedit` 或磁區寫入指令時，**必須實作 `DryRun` 開關（預設在開發測試環境為 `true`）**。
- 在 `DryRun = true` 模式下，僅印出將執行的指令腳本並模擬成功回傳，**絕對禁止在開發者的宿主機（特別是 Windows 家庭版筆電）直接改寫真實 BCD 開機扇區或格式化硬碟**。

### 4.2 以 IP 為核心的分組與網路身分還原
- **分組邏輯**：中控端以 `current_ip`（及手動配置的 `ip_list`）作為群組劃分依據，不得因兩台電腦的 `hostname` 相同而發生狀態覆蓋。
- **網路復原鐵則**：
  - 派送前必須強制確認已收到所有目標子機的 `NetworkIdentity` 快照（含 `STATIC` / `DHCP` 模式與 `original_hostname`），才允許啟動群播寫入。
  - 若 `ip_mode == "STATIC"`，派送後必須完整還原 IP、遮罩、預設閘道、DNS 與原始電腦名稱。
  - 若 `ip_mode == "DHCP"`，派送後必須將網卡切回 DHCP 自動分配，並還原原始電腦名稱。

### 4.3 客戶端密碼強制驗證
- 客戶端任何涉及狀態變更的 API 或 IPC 呼叫（切換還原點、新增還原點、刪除還原點、解除保護），若未攜帶有效管理員密碼驗證 Token 或中控端簽章，一律拒絕執行（HTTP `401 Unauthorized`）。
