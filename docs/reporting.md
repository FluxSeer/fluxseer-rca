# Reporting Architecture And Contracts

FluxSeer uses two separate report contracts. They are not the same test output
stored under different directories:

> **Internal Validation Report = test the product.**
>
> **User-facing Report = product output.**

Use these two names consistently in architecture, release evidence, and
contributor documentation. Do not call the user-facing catalog a “15/15 test
report”.

```text
                       FluxSeer RCA
                           │
                    Runtime execution
                           │
              ┌────────────┴────────────┐
              ▼                         ▼
      User-facing Report       Internal Validation Report
              │                         │
 fluxseer-riskrule-report/v1    fluxseer-test-report/v1
              │                         │
 RiskRule / Investigation       expected / actual
 RiskSignal / RCA state         assertions / differences
 evidence / verdict             side-effect checks
              │                         │
              ▼                         ▼
          User / AI                Maintainer / CI
```

## 兩個正式契約

### User-facing Report — `fluxseer-riskrule-report/v1`

使用者與 AI 分析使用的事件報告。它是 Kubernetes 公開 CR 的可攜式快照，包含：

- `RiskRule`
- 同規則產生的 `InvestigationRequest`
- 直接或 linked projection 的 `RiskSignal`

使用者透過 `fluxseer report riskrule ...` 取得 JSON 或 YAML。這個契約的內容必須與使用者對 Kubernetes API 有權限讀取的公開物件一致。

It answers: **What did FluxSeer observe, investigate, and conclude for this
RiskRule?** It does not contain test expectations, assertions, or PASS/FAIL.

### Internal Validation Report — `fluxseer-test-report/v1`

測試、CI 與 runtime validation 使用的共同外層。每份報告必須包含：

- suite/run identity
- aggregate result 與 passed/failed counts
- 每個 scenario 的 expected、actual、assertions、differences
- artifact 路徑與 side-effect 結果

測試套件專用資訊放在 `metrics` 或 `suiteSchemaVersion`，例如 provider request count、token、latency、workload kind coverage；不再新增另一個頂層報告契約。

JSON 是機器可讀的 source of truth；Markdown 是由 JSON render 的閱讀版本。

It answers: **Did FluxSeer behave according to the defined runtime contract?**
It is validation evidence, not an incident report for users.

## 不可互換的覆蓋數字

| Number | Meaning |
| --- | --- |
| **21** | Built-in RulePack Detection Patterns |
| **15/15** | Internal P0 runtime validation scenarios passed |
| **15** | User-facing RiskRule Report catalog examples |
| **2/2** | Internal canonical workload validation scenarios passed |

The 15 User-facing Reports are not 15 Detection Patterns. Policy rejection,
budget exhaustion, missing providers, insufficient evidence, NoIssueFound, and
unsupported retention are valid product report states, but they are not new
anomaly-detection knowledge.

The local 15-case catalog may consolidate exact reports from several PASS
runtime baselines. Its provenance manifest must identify each source artifact
and digest; “15 cases” must not imply one 15-scenario wall-clock test run.

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

## 使用者與維護者的邊界

使用者目前需要的報告類型只有 `fluxseer-riskrule-report/v1`。它描述實際
RiskRule 產生的公開異常物件，適合交給 AI 或下游系統。

以下 assertion 與 test-control context 只服務維護者，不應放入
User-facing Report：

- 合成 Event 與不完整證據案例；
- 故意錯誤設定與 validation failure；
- mock provider、Secret 與 egress policy failure；
- workload coverage、contract matrix 與 P0 aggregate gate。

同一個 runtime scenario 仍可能產生合法的 User-facing Report，例如
`ProviderDataPolicyDenied` 或 `RequiredEvidenceMissing`。該產品報告只描述
公開 CR 的實際狀態；預期值、PASS/FAIL、provider request 次數與禁止的副作用
則只存在於 Internal Validation Report。案例存在於 user-facing catalog 並不會
把它變成新的 Detection Pattern。
