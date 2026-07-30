# FluxAgent Mermaid Architecture Diagrams

This document is the maintained Mermaid diagram set for the current `v0.3` FluxAgent architecture.

Scope:

- current default path: read-only RCA through `RiskRule` and `InvestigationRequest`
- current optional integrations: Prometheus, Loki, Kubernetes Events, OpenAI API, Claude API, Gemini API, heuristic provider
- current guarded experimental path: `RemediationPlan` and `AgentAction`
- current release identity: `v0.3.0-beta.2`
- API group/version identity: `aiops.platform/v1alpha1`

The API group and version identity are fixed for the current `v0.3` line. The `v1alpha1` schema remains subject to compatible beta hardening.

The diagrams are intentionally Kubernetes-native. Hosted providers receive bounded evidence bundles; they do not receive cluster credentials or direct Kubernetes API access.

Important `v0.3.0-beta.2` boundary: direct `RiskRule` RCA receives gateway-level hosted-provider egress opt-in enforcement, but the canonical `InvestigationRequest` path remains the only path with full `status.execution.egressAudit` visibility.

## System Context

```mermaid
flowchart TB
    Operator[Human Operator]
    GitOps[GitOps Repository]
    ExternalAlert[External Alert Source]
    KubeAPI[Kubernetes API Server]
    FluxAgent[FluxAgent Controller Manager]
    Workloads[User Workloads]
    Events[Kubernetes Events]
    Prometheus[Prometheus]
    Loki[Loki]
    OpenAI[OpenAI API]
    Claude[Claude API]
    Gemini[Gemini API]
    Heuristic[Heuristic Provider]
    GHCR[GHCR]
    HelmOCI[Helm OCI Chart]
    HelmClient[Helm client / GitOps renderer]
    Runtime[Node container runtime]

    Operator -->|kubectl / CLI creates| KubeAPI
    GitOps -->|applies CRDs and manifests| KubeAPI
    ExternalAlert -->|future producer creates| KubeAPI

    KubeAPI <-->|watch / status update| FluxAgent
    FluxAgent -->|read-only list/get/watch| Workloads
    FluxAgent -->|read events| Events
    FluxAgent -->|bounded metric queries| Prometheus
    FluxAgent -->|bounded log queries| Loki
    FluxAgent -->|hosted RCA API call| OpenAI
    FluxAgent -->|hosted RCA API call| Claude
    FluxAgent -->|hosted RCA API call| Gemini
    FluxAgent -->|local no-secret RCA| Heuristic

    HelmClient -->|pull chart| HelmOCI
    HelmClient -->|render/apply manifests| KubeAPI
    Runtime -->|pull operator and demo images| GHCR
```

## Runtime Layers

```mermaid
flowchart TB
    subgraph L0["L0 Entry Points"]
        Manual[Manual InvestigationRequest]
        Rule[RiskRule]
        Alert[Future Alert Adapter]
        Legacy[Legacy Deployment annotation watcher]
    end

    subgraph L1["L1 Kubernetes API Contract"]
        DS[DataSource]
        RR[RiskRule]
        MP[ModelProvider]
        IR[InvestigationRequest]
        RS[RiskSignal]
        RP[RemediationPlan]
        AA[AgentAction]
    end

    subgraph L2["L2 Controllers"]
        DSRec[DataSourceReconciler]
        RRRec[RiskRuleReconciler]
        IRRec[InvestigationRequestReconciler]
        RSRec[RiskSignalReconciler]
        NotifyRec[RiskSignalNotificationReconciler]
        RPRec[RemediationPlanReconciler]
        AARec[AgentActionReconciler]
        LegacyRec[DeploymentRiskReconciler]
    end

    subgraph L3["L3 Domain Services"]
        InvSvc[investigation.Service]
        Detector[detector.Service]
        Gateway[modelgateway.Gateway]
        Guardrails[guardrails.Engine]
        ExecutorRouter[executor.Router]
        SnapshotStore[evidence.SnapshotStore]
        QueryPolicy[querypolicy]
        DataClass[dataclassification]
        Verifier[verifier]
        Metrics[rcametrics]
    end

    subgraph L4["L4 Adapter Interfaces"]
        DSRegistry[datasource.Registry]
        ProviderRegistry[model.Registry]
        SecretResolver[KubeSecretResolver]
        ProviderResolver[modelgateway.KubeResolver]
        TargetDiscovery[rule.DiscoverTargets / investigation resolveTarget]
    end

    subgraph L5["L5 External Integrations"]
        K8sEvents[Kubernetes Events]
        Prom[Prometheus]
        Logs[Loki]
        OTel[OpenTelemetry scaffold]
        CloudWatch[CloudWatch scaffold]
        Hosted[OpenAI / Claude / Gemini APIs]
        Local[Heuristic provider]
        Webhook[Webhook notifier]
    end

    Manual --> IR
    Rule --> RR
    Alert -.future.-> IR
    Legacy -.explicit opt-in.-> LegacyRec

    DS --> DSRec
    RR --> RRRec
    IR --> IRRec
    RS --> RSRec
    RS --> NotifyRec
    RP -.optional.-> RPRec
    AA -.optional.-> AARec

    DSRec --> DSRegistry
    RRRec --> DSRegistry
    RRRec --> TargetDiscovery
    RRRec --> Gateway
    RRRec --> RS
    RRRec --> IR
    IRRec --> InvSvc
    InvSvc --> DSRegistry
    InvSvc --> QueryPolicy
    InvSvc --> DataClass
    InvSvc --> SnapshotStore
    InvSvc --> Gateway
    InvSvc --> Verifier
    Gateway --> ProviderRegistry
    Gateway --> SecretResolver
    Gateway --> ProviderResolver
    RPRec --> Guardrails
    AARec --> ExecutorRouter
    NotifyRec --> Webhook
    LegacyRec --> Detector
    Detector --> DSRegistry

    DSRegistry --> K8sEvents
    DSRegistry --> Prom
    DSRegistry --> Logs
    DSRegistry -.scaffold.-> OTel
    DSRegistry -.scaffold.-> CloudWatch
    ProviderRegistry --> Hosted
    ProviderRegistry --> Local
```

## Kubernetes CRD Relationship

```mermaid
erDiagram
    DataSource {
        string spec_type
        string spec_endpoint
        object spec_networkPolicy
        object spec_queryPolicy
        object spec_dataClassification
        string status_phase
    }

    ModelProvider {
        string spec_provider
        string spec_model
        object spec_apiKeySecretRef
        object spec_dataPolicy
        object spec_fallbackProviderRef
        string status_phase
    }

    RiskRule {
        object spec_targetSelector
        duration spec_interval
        duration spec_window
        array spec_signals
        object spec_ai
        object spec_investigationPolicy
        string status_phase
    }

    InvestigationRequest {
        object spec_target
        object spec_timeRange
        string spec_mode
        array spec_dataSources
        array spec_queries
        object spec_modelProviderRef
        object spec_evidenceRequirements
        object spec_evidenceRetention
        object spec_queryBudget
        object spec_loopPolicy
        string status_outcome
        object status_execution
        array status_evidenceRefs
        object status_lineage
    }

    RiskSignal {
        object spec_target
        object spec_findingIdentity
        array spec_evidence
        string spec_actionType
        int spec_ttlSeconds
        object spec_parameters
        string status_phase
        string status_rcaSummary
    }

    RemediationPlan {
        object spec_target
        array spec_steps
        bool spec_dryRun
        string status_phase
    }

    AgentAction {
        object spec_target
        string spec_actionType
        object spec_parameters
        string spec_dryRunResult
        string spec_approvedBy
        string status_phase
    }

    RiskRule }o--o{ DataSource : "references signals.datasourceRef"
    RiskRule }o--o| ModelProvider : "references ai.providerRef"
    RiskRule ||--o{ RiskSignal : "materializes direct finding"
    RiskRule ||--o{ InvestigationRequest : "can create canonical request"
    InvestigationRequest }o--o{ DataSource : "references dataSources / queries.datasourceRef"
    InvestigationRequest }o--o| ModelProvider : "references modelProviderRef"
    InvestigationRequest ||--o| RiskSignal : "optionally creates linked discovered signal"
    RiskSignal ||--o| RemediationPlan : "optional guarded planning"
    RemediationPlan ||--o{ AgentAction : "materializes action state"
```

## API Class Diagram

```mermaid
classDiagram
    class ResourceStatus {
        +string phase
        +string message
        +int64 observedGeneration
        +Time updatedAt
    }

    class TargetRef {
        +string cluster
        +string namespace
        +string kind
        +string name
        +string apiVersion
        +string service
    }

    class DataClassification {
        +string level
        +string[] sensitivityTags
        +string source
        +string policyVersion
    }

    class EvidenceRef {
        +string id
        +string kind
        +string source
        +string summary
        +string query
        +string queryDigest
        +string contentDigest
        +string digestAlgorithm
        +string digestCanonicalization
        +string redactionProfile
        +DataClassification classification
        +bool truncated
        +string truncationReason
        +string limitDimension
        +int64 limit
        +int32 originalCount
        +int32 retainedCount
        +int32 originalBytes
        +int32 retainedBytes
        +PayloadRef payloadRef
    }

    class ModelProviderDataPolicy {
        +bool allowExternalTransmission
        +string[] allowedEvidenceKinds
        +string[] deniedSensitivityTags
        +bool allowLogSamples
        +bool requireRedaction
        +string maximumClassification
    }

    class InvestigationRequestSpec {
        +TargetRef target
        +InvestigationTimeRange timeRange
        +string question
        +LocalObjectReference[] dataSources
        +InvestigationQuery[] queries
        +LocalObjectReference modelProviderRef
        +string mode
        +EvidenceRequirements evidenceRequirements
        +EvidenceRetentionPolicy evidenceRetention
        +InvestigationQueryBudget queryBudget
        +InvestigationLoopPolicy loopPolicy
        +bool createRiskSignal
        +int64 ttlSeconds
    }

    class InvestigationRequestStatus {
        +string outcome
        +InvestigationFailure failure
        +RCAVerdict verdict
        +RCAClaim[] claims
        +RCAMissingEvidence[] missingEvidence
        +RCADegradation degradation
        +RCAExecution execution
        +EvidenceRef[] evidenceRefs
        +InvestigationLineage lineage
    }

    class RiskSignalSpec {
        +TargetRef target
        +FindingIdentity findingIdentity
        +string signalType
        +string severity
        +int confidence
        +EvidenceRef[] evidence
        +string actionType
        +bool dryRun
        +int64 ttlSeconds
        +map parameters
    }

    class RiskSignalStatus {
        +string rcaSummary
        +string rcaHypothesis
        +string rcaProvider
        +RCACause[] rcaCauses
        +Condition[] conditions
    }

    ResourceStatus <|-- InvestigationRequestStatus
    ResourceStatus <|-- RiskSignalStatus
    EvidenceRef --> DataClassification
    InvestigationRequestSpec --> TargetRef
    InvestigationRequestStatus --> EvidenceRef
    RiskSignalSpec --> TargetRef
    RiskSignalSpec --> EvidenceRef
    ModelProviderDataPolicy ..> EvidenceRef : evaluates classification
```

## Controller Ownership

```mermaid
flowchart LR
    subgraph Controllers
        DSRec[DataSourceReconciler]
        RRRec[RiskRuleReconciler]
        IRRec[InvestigationRequestReconciler]
        RSRec[RiskSignalReconciler]
        NotifyRec[RiskSignalNotificationReconciler]
        RPRec[RemediationPlanReconciler]
        AARec[AgentActionReconciler]
    end

    DSRec -->|owns status only| DSStatus[DataSource.status]
    RRRec -->|owns status and finding materialization| RRStatus[RiskRule.status]
    RRRec -->|creates / updates| RS[RiskSignal]
    RRRec -->|optional canonical mode| IR[InvestigationRequest]
    IRRec -->|owns canonical RCA status| IRStatus[InvestigationRequest.status]
    IRRec -->|optional discovered finding| RS
    RSRec -->|owns TTL and optional downstream transition| RSStatus[RiskSignal.status]
    NotifyRec -->|notification side effect only when configured| Notify[Webhook notification]
    RPRec -->|guardrails and approval state| RPStatus[RemediationPlan.status]
    RPRec -->|creates or updates action with Approved / WaitingApproval / Rejected state| AA[AgentAction]
    AARec -->|executes only when spec.approvedBy is set| AAStatus[AgentAction.status]
```

## Read-only RiskRule Flow

```mermaid
sequenceDiagram
    participant User as User / GitOps
    participant API as Kubernetes API
    participant RR as RiskRuleReconciler
    participant Resolver as Target Resolver
    participant DS as Datasource Registry
    participant GW as Model Gateway
    participant MP as ModelProvider
    participant RS as RiskSignal

    User->>API: apply RiskRule
    API-->>RR: reconcile RiskRule
    RR->>Resolver: resolve target selector
    Resolver-->>RR: matching workloads
    RR->>DS: validate datasource refs and queryType capabilities
    alt datasource or capability issue
        RR->>API: set RiskRule Ready=False / Degraded=True
        RR->>API: write partial RiskSignal when evidence exists
    else inputs ready
        RR->>DS: execute bounded configured signals
        DS-->>RR: query results
        RR->>GW: optional direct RCA if ai.rcaEnabled=true
        Note over RR,GW: v0.3.0-beta.2 direct RiskRule RCA applies gateway-level hosted-provider egress opt-in, but does not own canonical egressAudit status.
        GW->>MP: provider-neutral request
        MP-->>GW: normalized RCA response
        GW-->>RR: RCA result or degraded provider issue
        RR->>RS: create or update materialized RiskSignal
        RR->>API: update RiskRule.status
    end
```

## Canonical InvestigationRequest Flow

```mermaid
sequenceDiagram
    participant Producer as Operator / Alert / RiskRule
    participant API as Kubernetes API
    participant IRC as InvestigationRequestReconciler
    participant SVC as investigation.Service
    participant Target as Target Resolver
    participant DS as Datasource Adapters
    participant Policy as Query Policy and Budget
    participant Classify as Data Classification
    participant Egress as Provider Data Policy
    participant Gateway as Model Gateway
    participant Provider as Heuristic or Hosted Provider
    participant Verifier as Heuristic Verifier
    participant Signal as RiskSignal

    Producer->>API: create InvestigationRequest
    API-->>IRC: reconcile request generation
    IRC->>SVC: Preflight
    SVC->>Target: resolve target
    SVC->>DS: resolve datasource refs
    SVC->>Policy: validate query syntax, scope, limits
    alt validation failure
        IRC->>API: status.phase=Failed, outcome=Unknown
    else valid
        IRC->>API: status.phase=Collecting
        SVC->>DS: execute bounded evidence queries
        DS-->>SVC: raw results
        SVC->>Classify: normalize, classify, redact
        Classify-->>SVC: observations and EvidenceRefs
        SVC->>Egress: filter evidence kinds and evaluate hosted-provider policy
        alt provider data policy rejected
            IRC->>API: status.phase=Failed, outcome=Unknown, execution.egressAudit
        else policy allowed or local provider
        SVC->>Gateway: provider-neutral RCA request
        Gateway->>Provider: bounded provider execution
        Provider-->>Gateway: provider-native response
        Gateway-->>SVC: common RCA result
        SVC->>Verifier: verify claims against EvidenceRefs
        Verifier-->>SVC: bounded verdict and claim verification
        IRC->>API: write canonical InvestigationRequest.status
        opt createRiskSignal=true
            IRC->>Signal: create linked materialized RiskSignal
        end
        end
    end
```

## Evidence And Classification Pipeline

```mermaid
flowchart LR
    Raw[Raw adapter result]
    Normalize[Normalize to domain.Observation]
    Classify[Compute classification]
    Redact[Apply redaction profile]
    EvidenceRef[Persist compact EvidenceRef]
    Bundle[Build provider evidence bundle]
    Digest[Compute canonical bundle digest]
    Audit[Persist egress audit]
    Provider[Call provider only when policy allows]
    Status[InvestigationRequest.status]

    Raw --> Normalize
    Normalize --> Classify
    Classify --> Redact
    Redact --> EvidenceRef
    EvidenceRef --> Bundle
    Bundle --> Digest
    Digest --> Audit
    Audit --> Provider
    Provider --> Status

    subgraph ClassificationRules["Classification propagation"]
        DSDefault[DataSource default level]
        KindDefault[Evidence kind minimum level]
        Detection[Content detection tags]
        MaxLevel[highest level wins]
        UnionTags[sensitivity tags are unioned]
        NoDowngrade[redaction does not automatically lower classification]
    end

    DSDefault --> MaxLevel
    KindDefault --> MaxLevel
    Detection --> UnionTags
    MaxLevel --> Classify
    UnionTags --> Classify
    Classify --> NoDowngrade
    NoDowngrade --> Redact
```

## Hosted Provider Egress Decision

This diagram describes the canonical `InvestigationRequest` path in `v0.3.0-beta.2`.

```mermaid
flowchart TD
    Start[Provider-bound evidence bundle]
    ProviderType{Provider type?}
    LocalProvider[Local heuristic provider]
    LocalAudit[Persist local-provider execution metadata]
    LocalResult[Local provider result]
    Filter[Filter evidence by allowedEvidenceKinds]
    External{allowExternalTransmission?}
    Tags{Denied sensitivity tag present?}
    Level{classification within maximum?}
    Redaction{Required redaction completed?}
    Allowed[Decision: Allowed]
    Rejected[Decision: Rejected]
    Gateway[Call model gateway]
    ProviderFailure{eligible provider failure?}
    Fallback{Fallback provider configured?}
    ResolveFallback[Resolve fallback provider]
    FallbackPolicy[Re-evaluate fallback provider dataPolicy]
    FallbackAllowed{Fallback egress allowed or local?}
    FallbackAudit[Persist fallback attempt audit]
    FallbackCall[Call fallback provider]
    ProviderResult[Provider result]
    ProviderIssue[Provider issue propagated]
    Failed[status.phase=Failed outcome=Unknown]
    AllowedAudit[Persist allowed ProviderEgressAudit without raw evidence]
    RejectedAudit[Persist rejected ProviderEgressAudit without raw evidence]

    Start --> ProviderType
    ProviderType -- local heuristic --> LocalProvider
    LocalProvider --> LocalAudit
    LocalAudit --> LocalResult
    ProviderType -- hosted provider --> Filter
    Filter --> External
    External -- no --> Rejected
    External -- yes --> Tags
    Tags -- yes --> Rejected
    Tags -- no --> Level
    Level -- no --> Rejected
    Level -- yes --> Redaction
    Redaction -- no --> Rejected
    Redaction -- yes --> Allowed

    Allowed --> AllowedAudit
    AllowedAudit --> Gateway
    Gateway --> ProviderFailure
    ProviderFailure -- yes --> Fallback
    ProviderFailure -- no --> ProviderResult
    Fallback -- yes --> ResolveFallback
    Fallback -- no --> ProviderIssue
    ResolveFallback --> FallbackPolicy
    FallbackPolicy --> FallbackAllowed
    FallbackAllowed -- yes --> FallbackAudit
    FallbackAllowed -- no --> ProviderIssue
    FallbackAudit --> FallbackCall
    FallbackCall --> ProviderResult
    Rejected --> RejectedAudit
    RejectedAudit --> Failed
```

## Provider Adapter Conformance

```mermaid
flowchart LR
    Request[domain.ModelRequest]
    Gateway[modelgateway.Gateway]
    Registry[model.Registry]
    Heuristic[heuristic.Provider]
    OpenAI[openai.Provider]
    Claude[claude.Provider]
    Gemini[gemini.Provider]
    Native[Provider-native response]
    Schema[Provider response schema validation]
    Common[domain.ModelResponse]
    RCA[RCANormalizedResult]
    Status[InvestigationRequest.status.execution.providerResult]

    Request --> Gateway
    Gateway --> Registry
    Registry --> Heuristic
    Registry --> OpenAI
    Registry --> Claude
    Registry --> Gemini
    Heuristic --> Common
    OpenAI --> Native
    Claude --> Native
    Gemini --> Native
    Native --> Schema
    Schema --> Common
    Common --> RCA
    RCA --> Status
```

## Idempotent RCA Execution

```mermaid
flowchart TD
    Reconcile[Reconcile InvestigationRequest]
    ExecutionKey[Compute execution ID]
    KeyParts["request UID + generation + target + target UID + evidence bundle digest + provider type + provider generation + model + RCA schema version + reasoning policy version + canonicalization version"]
    Existing{Completed checkpoint exists for key?}
    Reuse[Reuse persisted provider result]
    Call[Start provider attempt]
    Attempt[Record attempt metadata]
    Result[Persist provider result digest]
    Verify[Run verifier]
    Terminal[Write terminal status]

    Reconcile --> ExecutionKey
    KeyParts --> ExecutionKey
    ExecutionKey --> Existing
    Existing -- yes --> Reuse
    Existing -- no --> Call
    Call --> Attempt
    Attempt --> Result
    Reuse --> Verify
    Result --> Verify
    Verify --> Terminal
```

## Evidence Retention Boundary

`EvidenceRef.query` may be persisted in `InvestigationRequest.status`; users should not place secret-bearing text in query strings. The beta.2 retention boundary avoids storing full raw adapter payloads, large raw logs, and provider prompts in status.

The `local-filesystem` normalized snapshot store is development-oriented in `v0.3.0-beta.2`: it is not encrypted, has no automatic snapshot garbage collection, and should not be treated as durable production evidence storage.

```mermaid
flowchart TB
    subgraph Status["InvestigationRequest.status"]
        Summary[Summary / verdict / claims]
        CompactRefs[Compact EvidenceRefs]
        PayloadRefs[Optional PayloadRefs]
        Digests[Query and content digests]
        Classification[Classification summary]
    end

    subgraph ExternalStorage["Optional external evidence storage"]
        Snapshot[Normalized snapshot only]
        LocalFS[local-filesystem store]
        Plaintext[PayloadRef.Encrypted=false]
        Expiry[expiry metadata only]
        NoGC[no automatic snapshot GC in beta.2]
    end

    subgraph NotStoredOrExported["Not stored in status or exported as labels"]
        FullRawPayload[Full raw adapter payload]
        FullPrompt[Full provider prompt]
        LargeRawLogs[Large raw logs]
        MetricLabels[Query or UID as metric label]
        SecretBearingQueries[Secret-bearing query text should not be used]
    end

    CompactRefs --> PayloadRefs
    PayloadRefs -.optional.-> Snapshot
    Snapshot --> LocalFS
    LocalFS --> Plaintext
    Snapshot --> Expiry
    Expiry --> NoGC
    Digests --> Snapshot
    Classification --> Snapshot
```

## Query Policy And Budget

```mermaid
flowchart LR
    Spec[InvestigationRequest.spec.queries]
    DSPolicy[DataSource.spec.queryPolicy]
    Budget[spec.queryBudget]
    Syntax[PromQL / LogQL syntax policy]
    Scope[Target scope requirement]
    Limits[Adapter-native result limits]
    Scheduler[Bounded query scheduler]
    Adapter[Datasource adapter]
    Degraded[Degraded partial evidence]

    Spec --> DSPolicy
    DSPolicy --> Syntax
    DSPolicy --> Scope
    Budget --> Limits
    Budget --> Scheduler
    Syntax --> Scheduler
    Scope --> Scheduler
    Scheduler --> Adapter
    Adapter --> Limits
    Limits --> Degraded
```

## Finding Identity And Lineage

```mermaid
flowchart TD
    Source[Source: RiskRule / Alert / Manual / RiskSignal]
    SourceUID[Source UID]
    SourceCoordinates[Source API version / kind / namespace / name]
    TargetUID[Target UID]
    TargetCoordinates[Target API version / kind / namespace / name]
    Attributes[Normalized finding attributes]
    FindingType[Finding type]
    Window[Window bucket]
    SourceGeneration[Source generation]
    TargetGeneration[Target generation]
    Logical[Logical finding identity]
    ObjectID[Object finding identity]
    Occurrence[Incident occurrence]
    Lineage[InvestigationRequest.status.lineage]
    Loop[Loop policy]

    Source --> Lineage
    SourceUID --> ObjectID
    TargetUID --> ObjectID
    FindingType --> ObjectID
    Attributes --> ObjectID
    SourceCoordinates --> Logical
    TargetCoordinates --> Logical
    FindingType --> Logical
    Attributes --> Logical
    ObjectID --> Occurrence
    SourceGeneration --> Occurrence
    TargetGeneration --> Occurrence
    Window --> Occurrence
    ObjectID --> Lineage
    Occurrence --> Lineage
    Lineage --> Loop
    Loop -->|maxDepth / block RiskSignal source by default| Lineage
```

## Optional Remediation Flow

```mermaid
sequenceDiagram
    participant RS as RiskSignal
    participant RSC as RiskSignalReconciler
    participant RP as RemediationPlan
    participant RPC as RemediationPlanReconciler
    participant Guard as guardrails.Engine
    participant AA as AgentAction
    participant AAC as AgentActionReconciler
    participant Exec as executor.Router

    Note over RS,AAC: Disabled by default. Requires explicit feature and RBAC profile. Current AgentAction approval and dry-run fields are experimental and tracked for hardening.
    RS-->>RSC: confirmed signal
    RSC->>RP: optional guarded plan creation
    RP-->>RPC: reconcile plan
    RPC->>Guard: evaluate action type, namespace, severity, approval
    RPC->>AA: create or update AgentAction with dryRunResult
    alt rejected by guardrail
        RPC->>RP: status.phase=Rejected
        RPC->>AA: status.phase=Rejected
        AA-->>AAC: reconcile rejected action
        AAC-->>AA: do not execute
    else manual approval required
        RPC->>RP: status.phase=WaitingApproval
        RPC->>AA: status.phase=WaitingApproval
        AA-->>AAC: reconcile action without spec.approvedBy
        AAC-->>AA: keep WaitingApproval
    else auto approved
        RPC->>RP: status.phase=Approved
        RPC->>AA: status.phase=Approved and spec.approvedBy set
        AA-->>AAC: reconcile approved action
        AAC->>Exec: execute or simulate
        Exec-->>AAC: result
        AAC->>AA: status.phase=Succeeded or Failed
    end
```

## Helm Deployment And RBAC Profiles

```mermaid
flowchart TB
    Values[Helm values.yaml]
    Chart[charts/kube-ai-sre]
    CRDs[crds/*.yaml]
    Deploy[controller Deployment]
    SA[controller ServiceAccount]
    RBAC[ClusterRole / Role / Bindings]
    Secrets[Provider Secret in controller namespace]
    SecretOwner[User / Secret manager]
    ReadOnly[default read-only profile]
    Remediate[experimentalExecutor profile]

    Values --> Chart
    Chart --> CRDs
    Chart --> Deploy
    Chart --> SA
    Chart --> RBAC
    SecretOwner --> Secrets
    RBAC -->|namespaced Secret reader| Secrets

    Values --> ReadOnly
    Values --> Remediate
    ReadOnly -->|default| RBAC
    ReadOnly -->|no workload mutation permissions| SA
    Remediate -->|requires remediation and executor features| RBAC
    Remediate -->|adds Job / ConfigMap / mutation permissions| SA
```

## Release And Publication Pipeline

This diagram records the `v0.3.0-beta.2` release path used by the project. The `release.yml` workflow is triggered by the annotated tag; the `test -> main -> tag` path is release discipline, not a hard workflow dependency.

```mermaid
flowchart LR
    TestBranch[test branch]
    MainBranch[main branch]
    Gate[verify-release-v0.3-beta]
    MainCI[main CI]
    Tag[annotated tag v0.3.0-beta.2]
    ReleaseWF[release.yml]
    Operator[GHCR operator image]
    Demo[GHCR demo-observability image]
    Chart[GHCR Helm OCI chart]
    GitHubRelease[GitHub prerelease assets]
    Provenance[Provenance verification]

    TestBranch --> Gate
    Gate --> MainBranch
    MainBranch --> MainCI
    MainCI --> Tag
    Tag --> ReleaseWF
    ReleaseWF --> Operator
    ReleaseWF --> Demo
    ReleaseWF --> Chart
    ReleaseWF --> GitHubRelease
    Operator --> Provenance
    Demo --> Provenance
    Chart --> Provenance
    GitHubRelease --> Provenance
```

## Component To Package Map

```mermaid
flowchart TB
    Cmd[cmd/operator]
    App[internal/operatorapp]
    Controllers[internal/controllers]
    Investigation[internal/investigation]
    Datasource[internal/datasource]
    Model[internal/model]
    Gateway[internal/modelgateway]
    Domain[internal/domain]
    QueryPolicy[internal/querypolicy]
    DataClass[internal/dataclassification]
    Verifier[internal/verifier]
    Metrics[internal/rcametrics]
    API[api/v1alpha1]
    Helm[charts/kube-ai-sre]
    Config[config]
    Examples[examples]

    Cmd --> App
    App --> Controllers
    App --> Datasource
    App --> Model
    App --> Gateway
    Controllers --> API
    Controllers --> Investigation
    Controllers --> Gateway
    Controllers --> Metrics
    Investigation --> Domain
    Investigation --> Datasource
    Investigation --> QueryPolicy
    Investigation --> DataClass
    Investigation --> Gateway
    Investigation --> Verifier
    Datasource --> Domain
    Model --> Domain
    Gateway --> Model
    Helm --> API
    Config --> API
    Examples --> Helm
```
