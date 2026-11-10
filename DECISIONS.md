# 架構決策記錄 (`DECISIONS.md`)

> [!IMPORTANT]
> 本文件對應 [`SPEC.md`](./SPEC.md) §1.2 的待決策清單 D1–D8。每一項都附上選項、取捨與**建議答案**。
> 決策未完成前，相關 Task 不得開工（見 [`TASKS.md`](./TASKS.md) 各 Task 的「前置」）。

## 文件資訊

| 項目 | 內容 |
| :--- | :--- |
| 版本 | v0.1（草稿） |
| 日期 | 2026-11-10 |
| 狀態 | **0 / 8 已決策** |
| 使用方式 | 在每個 Dx 的「決策」欄填入 `A` / `B` / `C`（可加註），並更新文末總表；完成後由 AI 依「決策後需同步」回填 SPEC / TASKS |

> [!NOTE]
> 標記 **（建議）** 的選項是基於目前已知條件的推薦，不是已定案。若你的環境條件不同（例如教室機器為 Windows 家用版、或網管不同意動 DHCP），請直接選其他選項並在「決策」欄註明理由。

---

## D1. 客戶端 Service 與本機 GUI 的分離方式、本機 IPC 契約

- **阻塞 Task**：2.3、3.2
- **背景**：Agent 是 LocalSystem 的 Windows Service，依 Windows Session 0 隔離**無法顯示任何 GUI**；但本機還原點面板必須顯示在登入使用者桌面。§4.3 又要求本機變更操作驗證管理員密碼，因此「服務 ↔ 面板」之間需要一條有授權機制的本機通道。[`AGENT.md`](./AGENT.md) §1.2 另要求避免 CGO、維持單一 `.exe`。

### 選項

**A（建議）單一 exe 雙模式 ＋ Named Pipe ＋ 純 Go WebView2 面板**

- `agent.exe --service`：LocalSystem，負責心跳、VHDX/BCD 操作、密碼驗證、指令執行。
- `agent.exe --ui`：使用者 session 啟動（托盤常駐 + WebView2 顯示面板），**不具任何特權**，所有變更都透過 pipe 轉送服務。
- 通道：`\\.\pipe\ezrestore-agent`，JSON-RPC（一行一訊息）。ACL 僅給 `SYSTEM` 與 `Administrators` 完全控制；一般使用者只被允許「唯讀查詢」方法。
- 授權：變更類方法一律帶使用者輸入的密碼，**由服務端以 bcrypt 驗證**後執行；不依賴「呼叫者是不是管理員」。
- 面板 UI：`jchv/go-webview2`（純 Go、無 CGO，Win10 1903+ / Win11 內建 Edge WebView2 runtime）載入與中控台共用元件庫的 SPA。

取捨：＋無 TCP port、無防火牆規則、無 CORS 問題；ACL 就是權限邊界；UI 與中控台共用元件。－ 依賴 WebView2 runtime（缺 runtime 的舊機需 fallback）；托盤 + WebView 的生命週期需處理。

**B 服務 ＋ 原生 Win32 面板（systray ＋ 對話框）**

取捨：＋零 runtime 依賴、體積最小、最穩。－ UI 一致性差、高 DPI／中文輸入／深色模式都要自己處理，開發量反而大於 A。

**C 服務內嵌 HTTP（127.0.0.1 + 隨機 port）＋ token 檔**

取捨：＋可完全重用 REST 經驗與前端。－ 本機任何程序都能連（token 檔 ACL 是唯一防線）、需處理 CORS 與 port 衝突，攻擊面比 named pipe 大。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§1.1 客戶端描述、新增「本機 IPC 契約」一節（pipe 名稱、方法清單、錯誤碼、ACL）。
- TASKS：2.3 / 3.2 移除「前置 D1」。
- AGENT：§2 技術棧（WebView2）、§3 目錄（`cmd/agentui` 或 `--ui` 模式說明）。

> [!WARNING]
> 無論選哪個選項，都必須在 SPEC 誠實寫明限制：**本機 Windows 管理員可停用服務或替換檔案來繞過密碼鎖**。密碼鎖防的是學生／一般使用者，不是管理員。不要把「防作弊」寫成規格承諾。

---

## D2. 引導進入 WinPE 的策略（PXE vs 本機開機項）

- **阻塞**：目前無 Task（SPEC §1.1 的 `pxe` worker 沒有對應 Task），實質阻塞 M4。
- **背景**：群播派送與部分還原點操作需要在 WinPE 進行。原規格以「PXE/WinPE」帶過，但教室環境的 DHCP/TFTP/交換器群播設定通常不受本專案控制，PXE 失敗時整個 M4 都無法驗證。

### 選項

**A（建議）本機 BCD ramdisk 開機 WinPE（無 PXE）**

- 把自建 WinPE 映像（`boot.wim`，含 Go agent、`udpcast`、網卡驅動）放在本機固定路徑（例如 `C:\EZRestore\pe\boot.wim`）或小型專用分割區。
- Agent 以 `bcdedit` 建立 `{ramdiskoptions}` 與 PE 開機項；收到 `CMD_ENTER_PE` 時設定下次開機項並重開機。
- WinPE 內由 agent 套用派送前保存的 `NetworkIdentity`（STATIC）或走 DHCP 取得網路。

取捨：＋完全不依賴 PXE/TFTP/DHCP 設定，不受交換器、防火牆、Secure Boot 的 NBP 簽章問題影響；**離線也能進 PE**（本機還原點操作可離線）。－ 每台多佔約 500–800 MB；需要建置 WinPE 映像（Windows ADK）；BCD 改寫屬高風險操作，必須 DryRun + 備份 BCD。

**B ProxyDHCP（Hub 兼作 PXE 伺服器）**

- Hub 在 67/4011 埠回應 PXE，提供 NBP 與 WinPE；不動既有 DHCP 伺服器。

取捨：＋救援／重裝時不需事先在機器上放 PE。－ 需固定 IP、關閉防火牆、UEFI 需對應 NBP（`ipxe.efi`）且 Secure Boot 需關閉或自行簽章；跨 VLAN 需 DHCP relay 設定；實務上多數教室網管不會同意。

**C 直接改現有 DHCP 的 option 66/67**

取捨：＋最單純。－ 需網管權限且影響整個網段，與其他 PXE 服務衝突，**不建議**。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§1.1（`pxe` 定位改為「B 才需要」）、§4.2 流程改用本機 PE 開機、新增「WinPE 映像需求」小節。
- TASKS：新增 Task「建置 WinPE 映像（agent + udpcast + 網卡驅動）與本機 PE 開機項管理」（建議排在 3.3 之前）。
- AGENT：§4.1 紅線加入「BCD 修改前必須備份並 DryRun」。

---

## D3. 派送映像的格式與 pipeline（udpcast 只是 byte pipe）

- **阻塞 Task**：4.2
- **背景**：`udp-sender` / `udp-receiver` 不懂磁碟分割與檔案系統，只負責搬 bytes。必須先決定「派送的單位」，否則 4.2 無法驗收，也無法估時。

### 選項

**A（建議，與 D4 的 VHDX 原生開機配套）映像 ＝ base VHDX 檔案，udpcast 檔案模式群播**

- 母機的 Windows 本來就住在 `base.vhdx`；派送 ＝ 把母機的 base 檔群播到各子機的同一固定路徑（例如 `D:\EZRestore\base.vhdx`）。
- 子機在 PE 中：接收為 `base.vhdx.new` → **SHA-256 校驗** → 寫入完成旗標 → 下次開機由 bootmgr 載入新 base。

取捨：＋不碰分割表/ESP/`bcdboot`，不需要 `partclone`；每台子機保有自己本地的 base 副本，可再衍生自己的差分鏈，與 D8 自然整合；檔案級可加校驗與續傳策略。－ 每台需容納一份 base（動態 VHDX 實際佔用通常小於最大值）；母機與子機的保存路徑必須一致。

**B partclone 分割區 block-level 串流**

- 母機 PE：`partclone.ntfs -c -s <win part> -o - | udp-sender --file -`；子機反向接收。

取捨：＋適合**非** VHDX 的傳統機器。－ 目標碟容量與分割配置必須一致、ESP/開機項要另外處理、無端到端校驗（需另加 sha256）、`partclone` 的 stdout 串接方式需實測。

**C wimlib WIM 映像**

取捨：＋跨容量、可壓縮、傳輸量小。－ 檔案級備份需重建開機項、注入驅動，工時最高；與「秒級還原點」是兩套不同機制。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§4.2 流程、§2.4 `DeployTask` 補 `image_sha256`、`image_bytes` 欄位。
- TASKS：4.2 拆為「傳檔 → 校驗 → 切換」三步；DryRun 必須聲明**不實際送出群播封包**。

---

## D4. 目標機 OS 版本要求與 VHDX 原生開機可行性

- **阻塞 Task**：3.3
- **背景**：Windows 的 VHDX **原生開機（native boot）僅支援 Pro / Enterprise / Education**；家用版（Home）不支援。若教室機器是家用版，M3 的「秒級還原點」整個不成立。另「把既有的實體 Windows 轉成 VHDX 原生開機」是必要的一次性建置流程，目前沒有任何 Task。

### 選項

**A（建議）統一 Windows 10/11 Pro 或 Education，UEFI+GPT，關閉 Secure Boot / Fast Startup / BitLocker，以 VHDX 原生開機為標準安裝**

- 新增一次性 Task「實體機 → VHDX 原生開機轉換」（WinPE 中建 VHDX、套用映像、以 `bcdboot`/`bcdedit` 建立開機項、首次開機驗證）。
- 動態 VHDX 需設定 `maximum` 上限，避免實體碟被吃滿。

**B 允許家用版共存**

- 家用版機器不做 native boot：只能走「群播整機派送」，沒有本機秒級還原點。
- 需在 SPEC 定義 `capability`（例如 `supports_restore_points: false`），UI 與 API 都依此隱藏/拒絕功能，避免使用者誤觸後才失敗。

**C 放棄 native boot，改用 VSS/diskshadow 快照 + 整碟複製**

取捨：＋任何版本都能用。－ 不是秒級、佔用大、切換要搬大量資料；與專案目標（秒級還原）衝突，**不建議**。

### 決策

> **決策**：＿＿＿（A / B / C，可加註；若選 B 請註明家用版機器數量/比例）

### 決策後需同步

- SPEC：新增「§1.3 環境需求（OS 版本、韌體、功能開關）」；§2.3 `ClientNode` 可能需 `capabilities` 欄位。
- TASKS：新增「實體機轉 VHDX 原生開機」Task；3.3 移除「前置 D4」。

---

## D5. Hub ↔ Agent 的傳輸安全與 Agent 身分驗證

- **阻塞 Task**：1.3、3.2
- **背景**：目前規格只有 `http://` 與 `ws://`；而 §3.2 會把「管理員密碼 Hash」派送到所有客戶端。同網段任何人可竊聽並離線破解，或偽造 Hub 下達「改密碼」指令。這是目前最大的安全性缺口。

### 選項

**A（建議）全站 HTTPS/WSS ＋ 自簽或內部 CA ＋ 憑證指紋釘選；Agent 使用 mTLS 客戶端憑證**

- Hub 首次啟動產生自簽 CA 與 server cert；Agent 安裝時寫入 CA 憑證或指紋（防中間人）。
- Agent 首次聯絡以「安裝 token」簽發 client cert，之後所有 WS 連線皆 mTLS，Hub 以此辨識 Agent 身分。
- 密碼同步改為：**TLS 通道內傳新密碼明文，由 Agent 自行 bcrypt** → 不再跨網路散佈 hash。

取捨：＋加密、雙向認證、防偽造指令，Go `crypto/tls` 原生支援。－ 憑證生命週期管理（輪替、過期）；瀏覽器第一次需信任自簽憑證（可匯入內部 CA 解決）。

**B 維持明文 ＋ 每個請求／WS 訊息 HMAC-SHA256 簽章 ＋ nonce/timestamp**

取捨：＋實作最快、不需憑證管理。－ 被動竊聽仍可取得管理員密碼與指令內容（含密碼同步），只解決「偽造」不解決「洩漏」。

**C 限定隔離網段、明文**

取捨：＋零成本。－ 風險自負；若採用，SPEC 必須寫明「網路隔離」為系統前提，且**不得**再用 hash 同步密碼。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§3 新增「通道安全」、§5.1/§5.2/§5.3 端點改 `https://` / `wss://`。
- TASKS：1.3、3.2 移除「前置 D5」；新增憑證產生／派發／輪替的實作項目。
- AGENT：§2 技術棧註明 `crypto/tls`、憑證存放位置。

---

## D6. Hub 管理後台的認證與授權機制

- **阻塞 Task**：1.3、2.2
- **背景**：§5.1 目前 16 個端點只有一句「需管理員憑證（待決策）」。若不定案，等於「任何人連上 8080 就能把整間教室洗掉」。

### 選項

**A（建議）單一管理員帳號 ＋ bcrypt（cost 12）＋ 伺服器端 Session**

- `POST /auth/login` 驗證後發 `HttpOnly + Secure + SameSite=Strict` 的 session cookie；session 存 SQLite，閒置 8 小時逾時。
- 變更類請求需附 CSRF token（同源 UI 可從 cookie 以外的 header 取得）。
- 登入失敗限流：5 次 / 5 分鐘（防暴力破解，也避免鎖死管理員）。
- 內嵌 UI 為同源，CORS 保持關閉（只允許同源）。

取捨：＋最小攻擊面、可即刻登出、WS 握手可直接驗證 cookie。－ 單節點假設（本專案 Hub 本來就是單一 exe，符合）。

**B JWT Bearer Token**

取捨：＋無狀態、易擴充。－ 前端需處理儲存與刷新（XSS 風險），登出較難即刻作廢。

**C Basic Auth**

取捨：＋最簡單。－ 每個請求都帶密碼、無法登出、WebSocket 握手難處理，不適合。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§3 新增「中控端認證」、§5.1 補 `auth/*` 的行為與 cookie 屬性、§5.2 WS 握手的驗證方式。
- TASKS：1.3、2.2 移除「前置 D6」；2.2 增加登入畫面。

---

## D7. 離線客戶端的 MAC 資料來源（WOL 的前提）

- **阻塞 Task**：3.1
- **背景**：`ClientNode.mac_address` 只會在有 Agent 心跳時才有。若一台機器**從未上線**（新機、重裝後未開機、群播前關機），Hub 沒有 MAC，WOL 直接無效。而 WOL 又常被用來「把關機的機器叫起來派送」，是雞生蛋問題。

### 選項

**A（建議）三層來源**

1. **Agent 上報**（主要）：在線時回報 MAC，Hub 持久化為最後已知值。
2. **首次建置匯入**：支援 CSV／DHCP lease 匯入（`ip,mac`）；或手動在 UI 補建。
3. **同 L2 ARP 掃描**（best effort）：Hub 對群組 IP 發 ARP/ICMP，補齊同網段的未知 MAC（跨 VLAN 無效）。

UI 規則：MAC 未知的機器「喚醒」按鈕停用並提示補建；WOL 廣播位址依群組所屬子網計算（不可只送 `255.255.255.255`）。

**B 只靠 ARP 掃描**

取捨：＋無需維護資料。－ 跨 VLAN／跨網段必失敗；需要 raw socket 或特權讀 ARP 表；離線機器若不在 ARP 快取也掃不到。

**C 只靠手動建檔**

取捨：＋最可靠、可稽核。－ 維運成本高，20 台以上容易漏。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§2.3 `ClientNode` 補 `mac_source`（`AGENT` / `IMPORT` / `ARP` / `MANUAL`）；§5.1 新增 MAC 匯入端點。
- TASKS：3.1 移除「前置 D7」，並擴充為「MAC 表 CRUD + 匯入 + 廣播位址計算」。

---

## D8. 「VHDX 差分還原點」與「母機群播派送」的關係

- **阻塞 Task**：3.3、4.2
- **背景**：SPEC 同時存在兩套機制，但從未說明它們如何共存。最常見的問題是：群播派送把子機的 base 換掉之後，原本的差分鏈怎麼辦？如果不定案，4.2 實作到一半就會發現資料模型衝突。

### 選項

**A（建議，與 D3-A 配套）派送 ＝ 重建各機 base；原有差分鏈全部作廢**

- 派送替換 `base.vhdx` 後，所有舊的 diff 節點失效並刪除；系統自動建立一個新的 base 還原點（`is_base=true`、`created_by=SYSTEM`，名稱如「派送後基準 2026-11-10」）。
- UI 發起派送前必須二次確認，並列出「將刪除 N 個還原點」。
- 還原點鏈深度從 0 重新開始。

取捨：＋語意單一、狀態不混合、實作最直接。－ 派送前的個人化設定（學生自己的資料）會消失——但這本來就是「還原教室」的預期行為。

**B 派送只更新 base、保留各機既有 diff**

取捨：＋可保留個人差異。－ 舊 diff 是「基於舊 base」的，換 base 後大概率不相容，可能直接無法開機；需額外的相容性檢查與回復流程，風險與工時都最高。

**C 兩套互斥：派送前強制刪除所有還原點，派送後由管理員手動建立基準點**

取捨：＋規則最嚴格不易誤用。－ 多一步人工操作，容易忘記而留下「沒有還原點」的機器。

### 決策

> **決策**：＿＿＿（A / B / C，可加註）

### 決策後需同步

- SPEC：§2.4 `DeployTask` 補 `resets_restore_points` 欄位或行為描述、§4.2 流程補「派送後基準點」步驟。
- TASKS：4.2 驗收補「派送後舊還原點已清除且存在新 base 點」；新增 Task「派送後自動建立基準還原點」。

---

## 決策總表（請填寫）

| 編號 | 題目 | 建議 | 你的選擇 | 備註 |
| :--- | :--- | :--- | :--- | :--- |
| D1 | 客戶端 Service / GUI 分離與 IPC | A（named pipe + WebView2） | ☐ | |
| D2 | 引導進入 WinPE 的策略 | A（本機 BCD ramdisk，無 PXE） | ☐ | |
| D3 | 映像格式與 pipeline | A（base VHDX 檔案 + udpcast 檔案模式） | ☐ | |
| D4 | 目標機 OS 版本與 native boot | A（統一 Pro/Edu） | ☐ | |
| D5 | 傳輸安全與 Agent 身分 | A（HTTPS/WSS + mTLS） | ☐ | |
| D6 | Hub 後台認證 | A（cookie session） | ☐ | |
| D7 | 離線 MAC 來源 | A（三層） | ☐ | |
| D8 | 兩套還原機制關係 | A（派送重建 base、舊鏈作廢） | ☐ | |

## 決策完成後的回填順序（由 AI 執行）

1. 更新 `SPEC.md` §1.2：移除已決策項目，並在對應章節補上定案內容。
2. 更新 `SPEC.md` §2 資料結構（D3 的 `image_sha256`、D4 的 `capabilities`、D7 的 `mac_source`、D8 的 `resets_restore_points`）。
3. 更新 `TASKS.md`：移除各 Task 的「前置 Dx」、新增缺漏 Task（WinPE 映像建置、實體機轉 VHDX、憑證管理、MAC 匯入、派送後基準點）。
4. 更新 `AGENT.md`：技術棧（WebView2、TLS）、目錄結構（`cmd/agentui`、`internal/workers/pxe` 的定位）。
5. 全部通過後，把本文件狀態改為「8 / 8 已決策」並 commit。

> [!WARNING]
> 提醒：D2 + D3 + D4 是一組的（本機 PE 開機 ＋ base VHDX 檔群播 ＋ native boot）。這三個若不一致（例如選 D4-B 家用版卻又選 D3-A），M4 會做不出來。填表時請一起看。
