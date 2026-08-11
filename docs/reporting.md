# 報告契約與收斂策略

專案目前將報告收斂成兩層，而不是讓每個測試套件各自發明頂層格式。

## 兩個正式契約

### `fluxseer-riskrule-report/v1`

使用者與 AI 分析使用的事件報告。它是 Kubernetes 公開 CR 的可攜式快照，包含：

- `RiskRule`
- 同規則產生的 `InvestigationRequest`
- 直接或 linked projection 的 `RiskSignal`

使用者透過 `fluxseer report riskrule ...` 取得 JSON 或 YAML。這個契約的內容必須與使用者對 Kubernetes API 有權限讀取的公開物件一致。

### `fluxseer-test-report/v1`

測試、CI 與 runtime validation 使用的共同外層。每份報告必須包含：

- suite/run identity
- aggregate result 與 passed/failed counts
- 每個 scenario 的 expected、actual、assertions、differences
- artifact 路徑與 side-effect 結果

測試套件專用資訊放在 `metrics` 或 `suiteSchemaVersion`，例如 provider request count、token、latency、workload kind coverage；不再新增另一個頂層報告契約。

JSON 是機器可讀的 source of truth；Markdown 是由 JSON render 的閱讀版本。

## 驗證入口

新的 JSON 報告統一使用：

```sh
bash hack/verify-report.sh path/to/report.json
```

驗證器會依 `schemaVersion` 分派到對應契約：

- `fluxseer-test-report/v1`
- `fluxseer-riskrule-report/v1`

舊的專用驗證器仍保留作為相容入口：`verify-test-report.sh` 與 `verify-riskrule-report.sh`。

## Legacy migration

`fluxseer-rca-quality-baseline-v1/v2/v3` 是歷史 suite schema。新的 quality baseline 已使用 `fluxseer-test-report/v1` 作為外層，並把原本的版本放在 `suiteSchemaVersion`；既有 durable artifact 不改寫，以保留歷史可追溯性。

早期 `fluxagent-runtime-*` 報告可能只有自訂 `summary.json` 或 Markdown。它們是 legacy evidence，不應作為新 runner 的模板；新增或重跑案例必須輸出 `fluxseer-test-report/v1`。使用者異常則必須使用 `fluxseer-riskrule-report/v1`，不能用測試摘要取代。

## 儲存邊界

- `reports/runtime/` 與 `reports/evaluation/`：本機、可重建、被 Git 忽略的原始工件。
- `docs/backlog/artifacts/`：需要長期審查的精簡 durable summary。
- Kubernetes CR：產品實際的使用者事件狀態；使用者報告由公開 CR 即時匯出，不把 `reports/` 當成事件資料庫。
