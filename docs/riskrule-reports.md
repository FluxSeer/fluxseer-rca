# RiskRule 異常報告

`RiskRule` 的使用者報告是 Kubernetes 公開資源的可攜式快照，不是測試程式自行產生的另一種異常資料。報告固定使用 `fluxseer-riskrule-report/v1`，並收錄：

- 被查詢的完整 `RiskRule`；
- 該規則建立的完整 `InvestigationRequest`；
- 該規則直接建立，或由調查結果投影出的完整 `RiskSignal`。

因此報告內的 `spec`、`status`、conditions、證據引用及資源身分，與有權限的使用者直接讀取 Kubernetes API 所取得的內容一致。報告只增加頂層的 schema 與查詢選擇資訊，不改寫 CR 的結果。

## 取得報告

輸出 JSON：

```sh
fluxseer report riskrule <riskrule-name> \
  --namespace <riskrule-namespace> \
  --output json > riskrule-report.json
```

輸出 YAML：

```sh
fluxseer report riskrule <riskrule-name> \
  -n <riskrule-namespace> \
  -o yaml > riskrule-report.yaml
```

執行者需要 `get` 指定的 `RiskRule`、`list` 同 namespace 的 `InvestigationRequest`、`list` 所有可見 namespace 的 `RiskSignal`，以及 `get` 被調查結果引用的 `RiskSignal`。Kubernetes RBAC 仍是報告可見範圍的權限邊界。

## 提供給 AI 分析

將單一 `riskrule-report.json` 提供給 AI，並指定要分析的問題即可，例如：

```text
請分析此 FluxSeer RiskRule 報告：
1. 說明觸發異常的證據與時間；
2. 區分已驗證、推測與缺失的資訊；
3. 根據 status.conditions 與 degradation.reasons 建議下一步；
4. 不要提出報告內容無法支持的根因。
```

報告可能包含工作負載名稱、namespace、查詢及證據摘要。交給外部 AI 服務前，使用者仍應依組織政策檢查敏感資訊。

## 與 `reports/runtime` 的差異

`reports/runtime/` 是維護者在測試站執行驗證後留下的本機工件，通常包含測試摘要、預期／實際比較、環境資訊，以及當次使用者報告的快照。它不是產品儲存使用者歷史異常的資料庫，而且預設不納入 Git。

其中 `incidents/*.json` 必須能由上面的 `fluxseer report riskrule` 指令重新取得；`summary.json` 與 `scenario-comparison.md` 則只用來證明測試案例是否符合契約。

使用者不需要閱讀 `reports/runtime/` 中的合成 Event、不完整證據、故意錯誤
設定或 mock provider failure。那些是維護者驗證工件；使用者只需取得
RiskRule 公開異常報告，或直接查詢自己的 `InvestigationRequest` 與
`RiskSignal` CR。

機器可讀契約定義於 [`test/reporting/riskrule-report.schema.json`](../test/reporting/riskrule-report.schema.json)。
