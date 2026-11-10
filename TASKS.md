# 任務分解與進度追蹤清單 (`TASKS.md`)

> [!NOTE]
> 本清單將 [`SPEC.md`](./SPEC.md) 拆解為可逐步驗證的開發任務。Vibe Coding 時每次只指定單一 Task（例如：「請依規格實作 Task 1.1」），完成並驗收後再推進下一項。
>
> **Milestone 編號以本文件為唯一權威**（SPEC §6 僅描述順序與依賴原則）。

## 使用公約

| 標記 | 意義 |
| :--- | :--- |
| `- [ ]` | 待辦 |
| `- [~]` | 進行中 |
| `- [x]` | 完成（已驗收） |
| `- [!]` | 阻塞（必須在敘述中寫明原因與待決事項連結） |

1. **一次一個 Task**：不跨 Task 順手重構；跨套件需求請先建新 Task。
2. **依賴先行**：任務標示的「依賴」未完成前不得開工（例外須在對話中明確同意並記錄）。
3. **完成定義（DoD）**：可編譯 ＋ 對應測試通過 ＋ 驗收條件逐項達成 ＋ [`AGENT.md`](./AGENT.md) §5 檢核全綠 ＋ 本文件勾選並補一行摘要（日期／commit）。
4. **規格優先**：若實作時發現 SPEC 缺漏或矛盾，**停止並提出修訂**，不得自行發明欄位、事件或端點（見 [`AGENT.md`](./AGENT.md) §1.4）。
5. **觸及範圍**：只修改該 Task 所屬套件／目錄；動到共用契約（`internal/protocol`、`SPEC.md`）必須在同一個 commit 內說明並更新對應文件。

---

## Milestone 0：環境與工具鏈

- [x] **Task 0.1：開發環境、測試機與工具鏈**
  - **目標**：建立可重現的開發環境：Go 1.26+、Node.js LTS + pnpm、Windows ADK（含 WinPE add-on）、Hyper-V（或實體測試機，Win11 Education、UEFI、關閉 Fast Startup／BitLocker）、Git；設定 `gofmt`／`golangci-lint`／`prettier`。
  - **依賴**：無
  - **驗收條件**：`go version` ≥ 1.26；`pnpm -v` 可用；`copype amd64 C:\WinPE_amd64` 成功產出 WinPE；測試 VM 可開機、可快照、可還原快照；`golangci-lint run` 在空專案上可執行。
  - **驗收摘要**（2026-11-11）：Go 1.27.0、Node 24.16、pnpm 12.15、Git 2.55、golangci-lint 1.64.6 安裝配置完成，.golangci.yml 與 .prettierrc 已就緒；空專案 linter 檢驗通過。

---

## Milestone 1：基礎建設與共享契約 (Foundation & Contracts)

- [x] **Task 1.1：專案骨架與共享資料模型 (`internal/protocol`)**
  - **目標**：`go mod init`、建立 [`AGENT.md`](./AGENT.md) §3 目錄；把 SPEC §2.1–§2.5 全部 JSON Schema 轉為 Go struct（含列舉、`null` 語意、`omitempty` 規則）；SPEC §2.6 轉移表以 `IsValidTransition` 驗證函式實作；`testdata/*.json` golden 樣本與 SPEC 範例逐位元組一致。
  - **依賴**：0.1
  - **驗收條件**：`go test ./internal/protocol/...` 通過；每個 struct 的序列化／反序列化與 SPEC 欄位 100% 吻合（含列舉值與 `null`）；非法狀態轉移必被拒絕且有表驅動測試；`go vet ./...` 無警告。
  - **驗收摘要**（2026-11-11）：建立 Go module 與標準目錄骨架；完成 models、events、api、transitions 與狀態機轉移驗證；57 項單元測試、golden JSON 比對與 golangci-lint/govet 全數通過。

- [x] **Task 1.2：SQLite 儲存層 (`internal/hub/store`)**
  - **目標**：`modernc.org/sqlite`（無 CGO）；資料表：`admins`、`sessions`、`clients`、`ip_groups`、`group_ips`（`UNIQUE(ip)`）、`restore_points`、`network_identities`、`mac_cache`、`deploy_tasks`、`audit_log`；WAL＋`busy_timeout`；`schema_version` 遷移機制；所有寫入走交易。
  - **依賴**：1.1
  - **驗收條件**：CRUD 單元測試；IP 屬於其他群組時回 `IP_IN_OTHER_GROUP`；`client_id` 穩定鍵唯一且不由 IP 推導；`go test -race` 通過併發寫入測試（本機無 C 工具鏈時於 CI 執行）；DB 可由舊 `schema_version` 升級。
  - **驗收摘要**（2026-11-11）：以 modernc.org/sqlite 實作無 CGO 儲存層與 10 張核心表；WAL 與 busy_timeout(5000)；schema_version 遷移升級機制；群組 IP 唯一性約束 (IP_IN_OTHER_GROUP) 與併發寫入測試全數通過。

- [x] **Task 1.3：Hub REST API（`internal/hub/server`）**
  - **目標**：實作 SPEC §5.1 全部端點（含 `auth/*`、批次語意、分頁、稽核查詢）；統一 `ok`／`data`／`error` 封套；CSRF 檢查；handler 僅做解析與回傳，業務邏輯放 service 層。
  - **依賴**：1.2
  - **驗收條件**：`httptest` 覆蓋每個端點的成功／失敗路徑；錯誤碼僅能是 §5.1 封閉集合；批次請求回傳 per-IP `results`；未登入回 `401`、缺 CSRF 回 `403`。
  - **驗收摘要**（2026-11-11）：實作 SPEC §5.1 全部 REST 端點與 Service/Handler 架構；Session Cookie 與 X-CSRF-Token 中介軟體；批次 per-IP 結果封套；401 未授權/403 CSRF 拒絕與 409 衝突等封閉錯誤碼 httptest 驗收全數通過。

- [x] **Task 1.4：Hub WebSocket 雙通道**
  - **目標**：`/ws`（中控台，SPEC §5.2 全部 11 種事件）與 `/ws/agent`（SPEC §5.3 的 8 種指令／6 種上報）；`seq` 單調遞增；`cmd_id`／`expires_at`／逾時作廢；非法狀態轉移回 `INVALID_STATE` 且**不得**寫入 DB。
  - **依賴**：1.3
  - **驗收條件**：WS 測試涵蓋所有事件與指令；`seq` 連續無跳號；未定義 `event` 被拒；非法轉移後 DB 無變化；重連後前端可取得全量（REST 契約配合）。
  - **驗收摘要**（2026-11-11）：以 coder/websocket 實作 /ws 與 /ws/agent 雙通道；單調遞增無跳號 seq；指令逾期管理；非法狀態轉移紅線防護（回絕且 DB 無變更）；71 項單元測試全數通過。

- [x] **Task 1.5：管理員認證與通道安全（決策 D5／D6）**
  - **目標**：bcrypt（cost 12）管理員密碼；Session Cookie（`HttpOnly`／`Secure`／`SameSite=Strict`）＋ CSRF；登入限流 5 次／5 分鐘；稽核日誌；自簽 CA 與 server cert 產生；`/agent/enroll` 以一次性 token 換發 client cert；mTLS 驗證；憑證到期前 30 天自動輪替。
  - **依賴**：1.3
  - **驗收條件**：登入／登出／逾時／限流／CSRF 皆有測試；明文埠預設關閉；無 client cert 的 Agent 連線被拒；輪替後舊憑證失效、Agent 以新憑證重連成功；稽核日誌含登入失敗與密碼變更。
  - **驗收摘要**（2026-11-11）：以 crypto/tls 與 x509 實作自簽 CA、server/client 憑證產生與 mTLS 雙向校驗；一次性 enroll token 防護；憑證 30 天輪替判定；限流與稽核記錄；mTLS 連線測試全數通過。

- [x] **Task 1.6：Mock Agent 模擬器（`cmd/mockagent`）**
  - **目標**：以同一份 WS 契約（mTLS）模擬 N 台不同 IP／MAC 的 Agent，可跳動狀態、可回應 `CMD_*`，並能驅動 SPEC §2.6 的合法／非法轉移。
  - **依賴**：1.4、1.5
  - **驗收條件**：20 台模擬可完整走 `READY_FOR_CAST → DEPLOYING → VERIFYING → POST_RESTORING_NET`；不得自訂訊息格式（與 `internal/protocol` 共用型別）；非法轉移可被觸發且 Hub 正確拒絕。
  - **驗收摘要**（2026-11-11）：實作 cmd/mockagent 模擬器，嚴格共用 internal/protocol 型別；20 台 Agent 併發執行完整派送狀態轉移與非法狀態轉移拒絕驗收通過。

- [x] **Task 1.7：契約同步（`cmd/gen-ts`）**
  - **目標**：由 Go struct 產生 `web/src/types/spec.ts`，避免雙寫；提交前檢查重跑無 diff。
  - **依賴**：1.1
  - **驗收條件**：重跑 codegen 產生零 diff；golden JSON 同時通過 Go 反序列化與 TS 型別檢查；刻意改動 Go 欄位會讓檢查失敗（負向測試）。
  - **驗收摘要**（2026-11-11）：實作 cmd/gen-ts 程式碼產生器；產出 web/src/types/spec.ts；重跑產生零 diff 與負向差異測試通過；M1 全量 73 項單元測試、linter 與 AGENT.md §5 檢核全綠。

---

## Milestone 2：中控台前端與本機鎖定面板 (Web Dashboard & Client UI)

> 任務可依依賴關係與 M3 交錯進行；不必嚴格照編號順序。

- [ ] **Task 2.1：React 前端骨架與 API/WS 客戶端**
  - **目標**：Vite + React + TypeScript `strict: true` + Tailwind；登入頁；WS 自動重連 Hook（重連後重抓全量）；API client 處理 `ok`／`error` 封套與 CSRF header；型別一律來自 `src/types/spec.ts`（Task 1.7 產物）。
  - **依賴**：1.7
  - **驗收條件**：`pnpm build` 與 `pnpm typecheck` 無錯；斷線重連後資料與 REST 全量一致；未登入時所有頁面導向登入。

- [ ] **Task 2.2：中控台「IP 群組與電腦狀態網格」**
  - **目標**：群組 CRUD（支援 IP 範圍或 IP 清單輸入）；電腦卡片顯示 IP、hostname、狀態、`active_point_id`、進度；**MAC 未知**提示與停用喚醒；`os_edition == Home` 時顯示不支援警示；批次選取與操作；派送精靈（前置檢查：NetworkIdentity 收妥、`pe_ready`、可用空間、無進行中任務）；任務頁（歷史／取消／重試）與稽核頁。
  - **依賴**：2.1、1.6
  - **驗收條件**：對 Mock Agent 可完成一次完整派送流程；MAC 未知時喚醒按鈕停用並回 `MAC_UNKNOWN`；批次操作結果逐台顯示；派送前檢查未通過時無法送出。

- [ ] **Task 2.3：客戶端本機鎖定面板（`--ui` 模式）**
  - **目標**：托盤常駐 + WebView2（`jchv/go-webview2`，純 Go）；IPC 客戶端（NDJSON，SPEC §3.4）；預設唯讀、變更操作每次輸入密碼；失敗鎖定提示；訂閱狀態事件；UAC 操作（更換 Hub／卸載）。
  - **依賴**：3.2（Agent 服務核心與 IPC 服務端）
  - **驗收條件**：唯讀方法免密碼可用；變更方法未帶密碼被拒；連續 5 次失敗鎖定；密碼錯誤提示不洩漏細節；WebView2 缺失時顯示安裝提示並退回托盤選單。

---

## Milestone 3：電源、PE 引導與 VHDX 還原點引擎 (Power, PE Boot & Snapshot Workers)

- [ ] **Task 3.1：WOL worker 與 MAC 資料來源（決策 D7）**
  - **目標**：`internal/workers/wol` 產生 102-byte Magic Packet；廣播位址依群組子網計算；MAC 來源三層（Agent 上報／CSV 匯入／ARP 補齊 best-effort）；MAC 未知時回 `MAC_UNKNOWN`。
  - **依賴**：1.2
  - **驗收條件**：封包 Byte 結構表驅動測試（6×`0xFF` + 16×MAC）；批量群組喚醒；廣播位址計算（含 /24、/16、/22）測試；未上線且無 MAC 的目標回 `MAC_UNKNOWN` 且 UI 可提示補建。

- [ ] **Task 3.2：Agent 服務核心與本機 IPC 服務端**
  - **目標**：Windows Service 安裝／啟動／停止／卸載；`client_id` 產生並持久化於保留區；`--enroll` 換發 client cert；WSS + mTLS 連線、`AGENT_HELLO`、15 秒心跳、指令框架（`cmd_id`／`expires_at`／`CMD_RESULT`）；`CMD_SYNC_PASSWORD`；本機 IPC 服務端（SPEC §3.4 方法表、pipe ACL、限流、稽核）。
  - **依賴**：1.5、1.2
  - **驗收條件**：服務可安裝／啟動／停止／卸載；重開機後自動重連；指令逾期作廢；IPC 每個方法皆有測試（唯讀免密碼、變更需密碼、5 次鎖定）；稽核日誌可查詢。

- [ ] **Task 3.3：VHDX 差分還原點引擎 (`internal/workers/vhdx`)**
  - **目標**：差分鏈管理（`depth ≤ 5`、`is_active` 唯一、`state` 機、`size_bytes` 計量）；Create／Switch／Delete；BCD 開機指標更新；`DryRun` 輸出可審閱的指令腳本與日誌；BCD 先備份後修改；以 volume GUID 定位保留區；深度或狀態不符回對應錯誤碼。
  - **依賴**：3.2
  - **驗收條件**：`DryRun` 產出的腳本有 golden 檔比對；測試 VM 上可完成 create／switch／delete；達深度上限回 `RESTORE_POINT_LIMIT`；刪使用中節點回 `INVALID_STATE`；`go test -race` 通過（同上，必要時於 CI 執行）。

- [ ] **Task 3.4：本機 PE 引導管理（決策 D2）**
  - **目標**：建立／更新／移除本機 PE 開機項（`{ramdiskoptions}` 指向保留區 `boot.wim`）；一次性 `bootsequence`；開機前備份 BCD 與失敗復原；`pe_ready` 判定與回報；全程 DryRun 可演練。
  - **依賴**：3.3
  - **驗收條件**：測試 VM 可一次性開進 WinPE，重開機後自動回 Windows；BCD 備份／還原可回滾一次刻意破壞；`DryRun` 下完全不呼叫 `bcdedit`；`pe_ready` 與實際狀態一致。

- [ ] **Task 3.5：WinPE 救援映像建置（決策 D2／救援 F1）**
  - **目標**：以 ADK 建置 amd64 UEFI WinPE；加入 `ezagent.exe`（PE 模式）、`udp-receiver`、網卡驅動；`startnet.cmd` 自動啟動 Agent；產出可放入保留區的 `boot.wim` 與可開機 USB；PE 內套用 `NetworkIdentity`（STATIC）或 DHCP 取得 IP。
  - **依賴**：3.2
  - **驗收條件**：實機以本機 PE 開機後 Agent 回報 `AGENT_HELLO`（`mode = "PE"`）；USB 版同樣可連上 Hub；STATIC 與 DHCP 各驗一次；PE 內可讀寫保留區（volume GUID 定位正確）。

- [ ] **Task 3.6：初次建置：實體機 → VHDX 原生開機（決策 D4）**
  - **目標**：腳本化 SPEC §4.4：建立動態 VHDX（設 `maximum`）與 GPT 分割、套用映像、`bcdboot` 建立 VHDX 開機項、複製 PE 映像並建立 PE 開機項、安裝 Agent、寫入 `client_id`；支援由母機群播給多台。
  - **依賴**：3.4、3.5
  - **驗收條件**：一台 VM／實體機完成建置並通過：可從 VHDX 開機進 Windows、`pe_ready = true`、還原點 create／switch／delete 可用、`DryRun=false` 時可正確進 PE；失敗時可從備份還原。

---

## Milestone 4：母機直傳群播與 IP/電腦名稱自記憶復原 (Direct Multicast & Network Preserve)

- [ ] **Task 4.1：網路身分快照擷取器 (`internal/workers/netpreserve/capture`)**
  - **目標**：讀取網卡模式（`STATIC`／`DHCP`）、IP、遮罩、閘道、DNS 與 `original_hostname`；產出 `NetworkIdentity`；同步上報 Hub 與寫入本機保留區（含校驗）。
  - **依賴**：3.2
  - **驗收條件**：本機實測抓取正確（STATIC 與 DHCP 各一）；保留區檔案在重開機後與 PE 內皆可讀；上報失敗時本機備份仍完整。

- [ ] **Task 4.2：母機對子機群播協調器 (`internal/workers/multicast`)**
  - **目標**：檔案模式群播——母機 `udp-sender --file <base.vhdx>`、子機 `udp-receiver --file base.vhdx.new`；解析 `--stat-period` 進度與速率並回報 `AGENT_PROGRESS`；SHA-256 校驗回報 `IMAGE_VERIFY_RESULT`；接收前空間檢查；`DryRun` **不得發出任何群播封包**。
  - **依賴**：3.5、3.6
  - **驗收條件**：`DryRun` 可模擬完整狀態流轉（`READY_FOR_CAST → DEPLOYING → VERIFYING`）且無封包外送；測試網段實測 2 台傳輸 1 GB 檔並校驗通過；中斷後可重試；校驗失敗回 `IMAGE_VERIFY_FAILED` 且**不切換** base；`NetworkIdentity` 未收妥時無法進入 `CASTING`。

- [ ] **Task 4.3：派送後網路身分復原器 (`internal/workers/netpreserve/restore`)**
  - **目標**：PE 內依 `NetworkIdentity` 離線注入：`STATIC` 還原 IP／遮罩／閘道／DNS；`DHCP` 清除母機殘留靜態設定並切回 DHCP；兩種情境皆還原 `original_hostname`；完成後寫入成功旗標供 Agent 判定。
  - **依賴**：4.2
  - **驗收條件**：STATIC／DHCP 兩情境腳本皆有 golden test；實機驗證重開機後以**原本的 IP／hostname** 連上 Hub；母機 IP 不殘留；注入失敗可重試且不破壞既有設定。

- [ ] **Task 4.4：派送後基準點建立與舊鏈清理（決策 D8）**
  - **目標**：派送成功後清除舊 diff 檔與 `restore_points` 記錄、建立新 base 點（`is_base = true`、`created_by = "SYSTEM"`、名稱含日期）、更新 `active_point_id`、推播 `RESTORE_POINTS_UPDATED`。
  - **依賴**：4.3
  - **驗收條件**：派送後舊節點全數移除、新 base 點存在且為唯一 `is_active`；`resets_restore_points` 行為符合 SPEC §2.4；UI 派送前的二次確認會列出將刪除的還原點數。

- [ ] **Task 4.5：端到端 DryRun 整合驗收**
  - **目標**：以 20 台 Mock Agent + 1 台母機模擬，跑完 `WOL → 快照 → PE → 群播 → 校驗 → 切換 → 還原 → ONLINE_WINDOWS`；另驗救援流程（USB PE 模擬）與三種失敗注入（斷線、校驗失敗、指令逾時）。
  - **依賴**：4.1、4.2、4.3、4.4
  - **驗收條件**：整合腳本可一鍵重跑；三種失敗情境皆正確回報（`DEPLOY_FAILED` 含 `failed_ips`）且不損壞既有 base；全流程時間與 SPEC 預估一致（±30%）；完成後更新本清單全部勾選與摘要。
