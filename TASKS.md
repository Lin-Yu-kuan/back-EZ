# 任務分解與進度追蹤清單 (`TASKS.md`)

> [!NOTE]
> 本清單將 [`SPEC.md`](file:///C:/Users/cree5/.gemini/antigravity/brain/33a0b0ec-4aab-4ca2-9f2b-b821da635c20/SPEC.md) 拆解為原子化、可逐步驗證的開發任務。透過 Vibe Coding 開發時，每次只需指定執行單一 Task（例如：「請依規格實作 Task 1.1」），完成並驗收後再推進下一項。

---

## Milestone 1：基礎建設與共享契約 (Foundation & Contracts)

- [ ] **Task 1.1：初始化專案骨架與共享資料模型 (`internal/protocol`)**
  - **目標**：建立 Go Module 與目錄結構，並將 `SPEC.md` 第 2 節的所有 JSON Schema（`NetworkIdentity`, `RestorePoint`, `ClientNode`, `IPGroup`, `WSMessage`）轉化為 Go Struct 與單元測試。
  - **驗收條件**：`go test ./internal/protocol/...` 通過，JSON 序列化/反序列化與 `SPEC.md` 欄位 100% 吻合。

- [ ] **Task 1.2：實作 SQLite 儲存層 (`internal/hub/store`)**
  - **目標**：使用純 Go SQLite 驅動建立資料表，支援儲存與查詢：IP 群組（`IPGroup`）、客戶端節點狀態、管理員密碼 Hash、以及派送前的 `NetworkIdentity` 備份表。
  - **驗收條件**：支援以 IP 清單建立群組（即使電腦名稱相同也不衝突），並通過 CRUD 單元測試。

- [ ] **Task 1.3：建立中控端 HTTP API 與 WebSocket Hub (`internal/hub/server`)**
  - **目標**：實作 `SPEC.md` 第 5 節定義的 REST API 與 `/ws` WebSocket 廣播中心，並內建 Mock 模擬器（可模擬 20 台不同 IP 客戶端連線與狀態跳動）。
  - **驗收條件**：啟動 Hub 後，可透過瀏覽器或測試腳本呼叫 API 並接收即時 WebSocket 狀態推播。

---

## Milestone 2：中控台前端與本機鎖定面板 (Web Dashboard & Client UI)

- [ ] **Task 2.1：初始化 React + TypeScript + Tailwind CSS 前端專案 (`web/`)**
  - **目標**：建立與 `SPEC.md` 完全一致的 `src/types/spec.ts` 型別定義，以及 WebSocket 自動重連 Hook。
  - **驗收條件**：`npm run build` 無任何 TypeScript 型別錯誤。

- [ ] **Task 2.2：實作中控台「IP 群組管理与電腦狀態網格」介面**
  - **目標**：
    1. 支援以 IP 範圍或 IP 清單新增/編輯群組（如「第 1 排」、「DHCP 組」）。
    2. 以卡片網格即時展示每台電腦的 IP、原本名稱、在線狀態、當前還原點與派送進度條。
  - **驗收條件**：可在 UI 上勾選單一 IP、特定 IP 群組或全選，並觸發批量操作。

- [ ] **Task 2.3：實作「客戶端本機還原點管理面板（含強制密碼鎖）」**
  - **目標**：設計客戶端本機介面，預設為唯讀鎖定狀態；點擊「切換還原點」、「新增還原點」或「刪除還原點」時，強制彈出管理員密碼輸入框，驗證通過才放行。
  - **驗收條件**：未輸入正確密碼時，所有變更操作均被攔截並提示權限不足。

---

## Milestone 3：電源控制與 VHDX 秒級還原點引擎 (Power & Snapshot Workers)

- [ ] **Task 3.1：實作區域網路喚醒模組 (`internal/workers/wol`)**
  - **目標**：輸入一組目標 MAC（由指定 IP 群組查表取得），向區域網路廣播端口發送標準 102-byte UDP Magic Packet。
  - **驗收條件**：單元測試驗證封包 Byte 結構正確（6 個 `0xFF` + 16 次重複 MAC），並支援批量群組喚醒。

- [ ] **Task 3.2：實作管理員密碼驗證與雙向心跳 (`internal/agent/auth`)**
  - **目標**：實作客戶端 Agent 連向中控端的註冊與心跳機制，以及 Bcrypt 密碼驗證與中控端遠端改密同步。
  - **驗收條件**：中控端修改密碼後，客戶端本機即刻套用新密碼驗證。

- [ ] **Task 3.3：實作 VHDX 差分還原點引擎 (`internal/workers/vhdx`)**
  - **目標**：封裝 `diskpart` 與 `bcdedit` 指令產生器，實作：
    1. 新增還原點（建立新差分層，檢查深度上限 $\le 5$）。
    2. 切換還原點（秒級重建乾淨讀寫層並更新開機指標）。
    3. 刪除還原點。
    4. **支援 `DryRun` 安全沙盒模式**（在家庭版開發筆電上模擬執行並輸出腳本日誌，不破壞真實開機區）。
  - **驗收條件**：在客戶端本機（輸入密碼後）與中控端皆能觸發新增、切換與刪除還原點，並通過單元測試。

---

## Milestone 4：母機直傳群播與 IP/電腦名稱自記憶復原 (Direct Multicast & Network Preserve)

- [ ] **Task 4.1：實作網路身分快照擷取器 (`internal/workers/netpreserve/capture`)**
  - **目標**：在 Windows 客戶端收到派送指令準備重啟前，自動讀取當前網卡是 `STATIC` 還是 `DHCP`，擷取 IP、子網遮罩、閘道、DNS 與當前電腦名稱，產出 `NetworkIdentity` JSON 並上傳中控端＋寫入本機保留區。
  - **驗收條件**：在本機實測可精準抓出當前筆電/電腦的真實網路配置與電腦名稱。

- [ ] **Task 4.2：實作母機對子機群播協調器 (`internal/workers/multicast`)**
  - **目標**：
    1. 中控端指定母機 IP（Sender）與目標子機 IP 清單（Receivers）。
    2. 封裝 `udp-sender` 與 `udp-receiver` 進程控制，解析標準輸出（Stdout）中的傳輸速率（MB/s）與完成百分比（%），即時透過 WebSocket 推播至中控台。
  - **驗收條件**：透過 `DryRun` 流模擬器驗證母機與多台子機從 `READY_FOR_CAST` 到 `DEPLOYING` 100% 的完整狀態流轉。

- [ ] **Task 4.3：實作派送後 IP 與電腦名稱自動復原器 (`internal/workers/netpreserve/restore`)**
  - **目標**：當子機接收完母機映像檔後，根據派送前保存的 `NetworkIdentity` 自動產生並執行復原動作：
    - 若原為 `STATIC`：自動設定回原本的固定 IP、遮罩、閘道、DNS，並還原原本的電腦名稱。
    - 若原為 `DHCP`：清除母機殘留的固定 IP，切回 DHCP 自動分配，並還原原本的電腦名稱。
  - **驗收條件**：驗證 `STATIC` 與 `DHCP` 兩種情境下的復原腳本生成與執行邏輯 100% 正確，杜絕母機 IP 感染衝突。
