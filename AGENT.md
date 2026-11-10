# AI 開發守則與架構約束 (`AGENT.md`)

> [!IMPORTANT]
> 本文件為 AI Agent 在進行本專案開發時的最高行為準則。每次撰寫或修改程式碼前，必須對照 [`SPEC.md`](./SPEC.md) 與 [`TASKS.md`](./TASKS.md)，嚴禁偏離契約或自行發明未定義的欄位。

## 0. 文件資訊

| 版本 | 日期 | 狀態 |
| :--- | :--- | :--- |
| v1.0 | 2026-11-11 | 已定案（對應 SPEC v1.0、DECISIONS D1–D8 全採 A） |

- 相關文件：[`SPEC.md`](./SPEC.md)、[`TASKS.md`](./TASKS.md)、[`DECISIONS.md`](./DECISIONS.md)

---

## 1. 角色定位與開發哲學

1. **規格優先（Spec-First）**：所有 JSON 欄位名稱、狀態機枚舉值、REST 路徑、WebSocket 事件名稱、錯誤碼，100% 以 `SPEC.md` 為準，禁止自行改名（例如將 `ip_mode` 改為 `ipType`）。
2. **薄核心 ＋ 獨立 Worker（Thin Core + Workers）**：
   - 拒絕過度設計（No Over-engineering）：**嚴禁使用 Go 動態 `plugin` 套件、反射（Reflection）依賴注入框架或 ORM**。
   - 以標準 Go package 做編譯期解耦；維持無 CGO、單一 `.exe`、零外部 runtime 依賴（WebView2 為系統內建元件，見 §2）。
3. **增量可驗證（Incremental & Testable）**：每完成 `TASKS.md` 的一個 Task，必須可編譯、通過對應測試，且 §5 的檢核全綠；不做「先寫起來放」的未驗證程式碼。
4. **缺口處理協議（Spec-Gap Protocol）**：實作中若發現 SPEC 缺漏、矛盾或不可行：
   1. **停止**該部分實作，不得先寫假實作或 TODO 繞過。
   2. 在回覆中明確指出「哪一條 SPEC、缺什麼、影響哪個 Task、建議怎麼補」。
   3. 經確認後**先改 SPEC**（必要時同步 `DECISIONS.md`），再改程式碼；兩者必須在同一個 commit 或相鄰 commit 內。
   4. 禁止用「先照我理解做，之後再對齊」的方式推進契約性內容。

---

## 2. 技術棧、版本與依賴政策

| 層級 | 選定技術 | 規範與限制 |
| :--- | :--- | :--- |
| **中控端後端 (Hub)** | Go 1.26+（`go.mod` 固定版本） | `net/http`、`github.com/coder/websocket`、`modernc.org/sqlite`（無 CGO）、`crypto/tls`、`golang.org/x/crypto/bcrypt`、`golang.org/x/sys/windows/svc` |
| **中控台前端 (Web UI)** | React + TypeScript（`strict: true`）+ Vite + Tailwind CSS | 套件管理器 **pnpm**；Node.js LTS；型別一律由 `cmd/gen-ts` 產生，不得手寫 `src/types/spec.ts` |
| **客戶端代理 (Agent)** | Go 1.26+，Windows Service ＋ `--ui` | `golang.org/x/sys/windows/svc`；WebView2 以 `github.com/jchv/go-webview2`（純 Go、無 CGO）；本機 IPC 為 Named Pipe（§4.3） |
| **引導與影像引擎** | Windows ADK（WinPE）、`bcdedit`／`diskpart`、`udpcast` | 檔案模式群播；**不使用 PXE／DHCP／TFTP**；所有磁碟／BCD 操作必須支援 `DryRun` |
| **資料庫** | SQLite（WAL 模式） | 自寫 SQL ＋ `schema_version` 遷移；單一 writer；禁止 ORM |

**版本與依賴規則**
- 主要版本一律寫進 `go.mod`／`package.json`／`pnpm-lock.yaml`；不得使用 `latest` 或浮動範圍。
- 新增任何第三方依賴（含前端）必須：① 在 commit message 說明理由；② 確認授權可接受；③ 優先選無 CGO、維護中的套件；④ 同步更新本表。
- **禁止**：CGO 依賴、Go `plugin`、反射型 DI 框架、ORM、前端狀態管理大禮包（如需要，先提 Task）。
- 外部二進位（`udpcast`、ADK 產物）不得於執行期下載；隨安裝包提供，並記錄版本與 SHA-256 於 `scripts/deps.lock`。

---

## 3. 專案標準目錄結構

```text
back-EZ/                        # 專案根目錄（Go module 路徑：github.com/<owner>/back-ez）
├── SPEC.md                     # 系統技術規格白皮書（SSOT）
├── AGENT.md                    # AI 開發守則（本文件）
├── TASKS.md                    # 任務分解與進度追蹤
├── DECISIONS.md                # 架構決策記錄
├── cmd/
│   ├── hub/                    # 中控端主程式入口
│   ├── agent/                  # 客戶端主程式入口（--service / --enroll / --ui）
│   ├── gen-ts/                 # Go struct → web/src/types/spec.ts 產生器
│   └── mockagent/              # 開發用 Mock Agent 模擬器
├── internal/
│   ├── protocol/               # 與 SPEC §2 1:1 對應的 Go 型別、列舉、狀態轉移驗證
│   ├── hub/
│   │   ├── server/             # REST handlers、路由、middleware（CSRF／session）
│   │   ├── store/              # SQLite schema、遷移、CRUD
│   │   └── ws/                 # /ws（中控台）與 /ws/agent（Agent）兩條通道
│   ├── agent/
│   │   ├── service/            # Windows Service 生命週期、指令執行框架
│   │   ├── secure/             # bcrypt、憑證、enroll、密碼同步
│   │   └── ipc/                # Named Pipe 伺服器／客戶端（NDJSON）
│   └── workers/
│       ├── wol/                # WOL Magic Packet、廣播位址計算
│       ├── vhdx/               # 差分 VHDX 鏈、BCD 開機指標、DryRun 腳本
│       ├── netpreserve/        # capture/ 與 restore/（網路身分快照與注入）
│       └── multicast/          # udp-sender / udp-receiver 控制與進度解析
├── web/                        # React 中控台（內嵌至 Hub）與本機面板共用元件
│   └── src/types/spec.ts       # codegen 產物（禁止手改）
├── testdata/                   # golden JSON／DryRun 腳本樣本
└── scripts/                    # winpe-build、install、e2e、deps.lock
```

> [!NOTE]
> `internal/workers/pxe` **不建立**：PXE 引導已排除於 M1–M4（見 SPEC §1.3）。若未來重啟該決策，先更新 `DECISIONS.md`。

---

## 4. 關鍵領域開發紅線（嚴格遵守）

### 4.1 磁碟與開機指令防護（Safety Sandbox）

- `vhdx`、`multicast`、`netpreserve/restore` 在呼叫 `diskpart`、`bcdedit`、格式化或磁區寫入前，**必須**經過 `DryRun` 開關（開發／測試環境預設 `true`）。
- `DryRun = true` 時僅輸出可審閱的指令腳本與日誌，**模擬成功回傳，絕不執行**。
- 關閉 DryRun（`dry_run = false`）必須滿足全部條件，否則程式必須拒絕執行：
  1. 由設定檔明確設定（不得由 CLI 臨時覆寫，除非同時帶 `--i-understand-risk`）。
  2. 環境為 WinPE 或已通過測試機白名單檢查。
  3. 目標磁碟簽章在允許清單內；**系統碟／開機碟**不在清單時直接拒絕。
- 修改 BCD 前必須 `bcdedit /export` 備份，失敗時自動還原；修改後驗證開機項存在。
- 絕對禁止在開發者宿主機（特別是 Windows Home 筆電）以 `dry_run = false` 執行破壞性操作。
- 所有破壞性指令（實際執行時）寫入不可竄改的稽核日誌（時間、磁碟簽章、完整命令、結果）。

### 4.2 以 IP 為核心的分組與網路身分還原

- **分組邏輯**：以 `current_ip` 與 `IPGroup.ip_list` 為準；`client_id` 為穩定鍵，禁止由 IP／MAC 推導；禁止用 `hostname` 當唯一鍵。
- **網路復原鐵則**：
  - 派送前必須確認**所有**目標子機的 `NetworkIdentity` 已收妥（含 `STATIC`／`DHCP`、`original_hostname`），否則不得啟動群播。
  - `ip_mode == "STATIC"`：派送後完整還原 IP、遮罩、預設閘道、DNS 與原始電腦名稱。
  - `ip_mode == "DHCP"`：清除母機殘留靜態設定、切回 DHCP，並還原原始電腦名稱。
  - 還原流程失敗時**不得**留下母機 IP（寧可回 DHCP 並回報錯誤）。

### 4.3 客戶端密碼與本機 IPC

- 本機任何涉及狀態變更的操作，一律經 Named Pipe 送到 `--service` 程序，由服務端以 bcrypt 驗證密碼；`--ui` 程序**不得**持有密碼雜湊、不得快取密碼。
- 密碼不得經由命令列參數、環境變數或暫存檔傳遞；IPC 訊息不得寫入日誌。
- 遠端操作的安全性邊界是「Hub 管理員 session ＋ mTLS 指令」；Agent 必須驗證 client cert 綁定的 `client_id` 與指令目標一致，不符即拒絕。
- 驗證失敗限流（5 次／5 分鐘）與稽核（時間、來源通道、結果）必須實作；錯誤訊息不得區分「帳號不存在／密碼錯誤」。
- 文件與 UI 必須誠實標示：**本機 Windows 管理員可停止服務繞過密碼鎖**；密碼鎖防護對象是一般使用者。

### 4.4 通道安全與憑證

- 正式環境**禁止**任何明文 HTTP／WS；不得關閉憑證驗證（禁止 `InsecureSkipVerify`）。
- 私鑰檔案權限僅限服務帳號；憑證輪替不得造成連線中斷（先建立新憑證再汰換舊憑證）。
- 密碼相關內容（含新密碼明文）僅能在 TLS／mTLS 通道內傳輸；**禁止傳送密碼雜湊**（見 SPEC §3.5）。
- `POST /api/v1/agent/enroll` 是唯一的明文入口，必須以一次性 token ＋ 內網限制保護，token 使用後立即失效。

### 4.5 映像完整性與群播

- 群播前由 Hub 取得母機 `base.vhdx` 的 SHA-256 與大小；子機接收完成後**必須**校驗一致才能切換，校驗失敗一律 `IMAGE_VERIFY_FAILED` 且不得 rename／切換。
- `DryRun` 模式下**不得發出任何群播封包**（避免灌爆開發環境網段）。
- 接收前必須檢查保留區可用空間 ≥ `image_bytes × 1.05`；不足即拒絕任務，不得邊傳邊爆碟。
- 保留區檔案一律以 volume GUID／磁碟簽章定位，**禁止**使用磁碟機代號（PE 與 Windows 不同）。
- 群播只傳輸檔案位元組，不得在傳輸層解析或改寫內容。

### 4.6 資料庫、稽核與個資

- SQLite 一律 WAL ＋ `busy_timeout`；寫入集中於單一 writer，避免 `SQLITE_BUSY`。
- 所有管理員與遠端變更寫入 `audit_log`（誰、何時、對哪台、什麼操作、成功或失敗）；稽核不得記錄密碼、雜湊或完整 `NetworkIdentity` 的敏感欄位（僅記 IP／hostname）。
- API 回應與日誌**不得**包含 `password_hash`。
- `client_id`、MAC、hostname 屬可識別資訊，除錯日誌需遮罩或僅記後四碼。

---

## 5. 提交前檢核（必跑，全部通過才可 commit）

```bash
gofmt -l .                  # 必須無輸出
go vet ./...
go test ./...
go test ./... -race         # 需 CGO 工具鏈（Windows 需 mingw）；本機無此工具鏈時改在 CI（Linux）執行
golangci-lint run
pnpm -C web typecheck
pnpm -C web build
go run ./cmd/gen-ts         # 產生後 git diff --exit-code 必須為空
```

- 若改動 `internal/protocol` 或 `SPEC.md` 契約：額外跑 `go test ./internal/protocol/...`（golden JSON）與 `cmd/gen-ts`，並確認前端型別同步。
- 若改動 `vhdx`／`multicast`／`netpreserve`：額外確認 `DryRun` 路徑的 golden 腳本測試通過。

---

## 6. 契約同步與測試策略

- **唯一來源**：Go struct（`internal/protocol`）為契約的程式化呈現；`web/src/types/spec.ts` 由 `cmd/gen-ts` 產生，禁止手改。
- **事件與路徑**：WebSocket 事件名稱、REST 路徑、錯誤碼只能來自 SPEC §5；新增或修改必先改 SPEC。
- **測試層次**：
  1. 單元測試（table-driven）——純函式、狀態轉移、封包結構。
  2. Golden 測試——JSON 序列化、`DryRun` 指令腳本、`spec.ts` 產物。
  3. 整合測試——`httptest` ＋ 真實 WS 連線 ＋ 暫存 SQLite。
  4. 端到端（`DryRun`）——Task 4.5 的 20 台模擬流程。
- 未測試的錯誤路徑不得上線；每個 `error.code` 至少一個測試。
- 禁止用 `time.Sleep` 當同步手段；測試中一律用 channel／`Eventually` 模式。

---

## 7. 文件同步與工作流程

每個 Task 的標準流程：

1. **讀**：SPEC 對應章節 ＋ `TASKS.md` 該 Task ＋ 本文件 §4 紅線。
2. **確認**：依賴是否完成？是否需要先補 SPEC（§1.4）？
3. **實作**：只動該 Task 範圍；新增檔案位置依 §3。
4. **驗證**：跑 §5 全部指令；跑該 Task 的驗收條件。
5. **回填文件**：
   - 把 `TASKS.md` 對應 checkbox 改為 `[x]`，並補一行摘要（日期／commit／一句話結果）。
   - 若契約有變：同一個 commit 更新 `SPEC.md`；若屬新的架構選擇，另補 `DECISIONS.md`。
6. **Commit**：一個 Task 一個 commit（例外：純文件修正）；訊息格式 `type(scope): summary`，內文說明驗收方式。

**工作流禁止事項**
- 不得同時開多個 Task 於同一工作區（會造成契約衝突與驗證失真）。
- 不得把 `TASKS.md` 的 Task 直接跳過驗收就勾選。
- 不得在文件裡留下「待補」而無對應 Task 或 `DECISIONS.md` 項目。

---

## 8. 命名與術語

**Worker 命名對照（唯一合法名稱）**

| 概念名稱（＝Go package） | 目錄 | 對應 Task |
| :--- | :--- | :--- |
| `wol` | `internal/workers/wol` | 3.1 |
| `vhdx` | `internal/workers/vhdx` | 3.3 |
| `netpreserve`（`capture`／`restore`） | `internal/workers/netpreserve` | 4.1、4.3 |
| `multicast` | `internal/workers/multicast` | 4.2 |
| `pxe` | **不建立**（backlog，見 SPEC §1.3） | — |

- 舊稱 `wol_worker`／`vhdx_worker`／`pxe_worker`／`multicast_worker`／`net_preserve_worker` 一律廢止，不得再出現在程式碼、註解、文件或 JSON 欄位中。
- 術語固定：**中控端**＝Go Hub 本體；**中控台**＝其 Web UI；**Agent**＝客戶端 Windows 服務；**本機面板**＝`--ui` 模式；**母機／子機**＝派送中的 Sender／Receiver。禁止用「中控台」指稱後端。
- 程式命名：Go 檔案用 `snake_case.go`、型別 PascalCase、JSON tag 用 SPEC 的 `snake_case`；TS 型別與 Go struct 同名。
- 錯誤碼只使用 SPEC §5.1 的封閉集合（IPC 另加 `PASSWORD_REQUIRED`、`RATE_LIMITED`）。
