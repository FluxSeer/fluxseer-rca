# Repository Engineering Instructions

All meaningful architecture, implementation, refactoring, testing,
performance, compatibility, RCA/RiskRule, integration, and release work must
follow the repository skill:

```text
$engineering-baseline
```

The authoritative workflow is
[`.agents/skills/engineering-baseline/SKILL.md`](.agents/skills/engineering-baseline/SKILL.md).

## Non-negotiable rules

- Correctness takes precedence over performance and implementation speed.
- Never fabricate RCA evidence or unsupported conclusions.
- Separate observed evidence from inference and hypothesis.
- Report insufficient evidence when the data cannot support a conclusion.
- Do not silently break CRDs, APIs, CLI/config, Helm values, metrics, reports,
  public Go APIs, persisted formats, or integration contracts.
- Keep provider-specific behavior behind adapter boundaries where appropriate.
- Do not claim performance improvements without measurements.
- Do not claim Kubernetes/provider compatibility without verification.
- Do not weaken tests, security, or safety checks merely to make CI pass.
- State clearly what was verified and what remains unverified.

Before declaring meaningful work complete, apply the skill's Definition of Done
and include its verification summary.
