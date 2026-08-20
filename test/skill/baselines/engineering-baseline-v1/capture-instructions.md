# Local Codex capture instructions

You are producing one independent behavioral qualification capture for the
FluxSeer RCA `engineering-baseline` Skill.

Read these repository sources before evaluating the corpus:

- `AGENTS.md`
- `.agents/skills/engineering-baseline/SKILL.md`
- `test/skill/engineering-baseline-evals.yaml`
- `test/skill/README.md`

Evaluate every one of the 13 corpus cases exactly once. Apply the Skill to
meaningful engineering cases and do not activate it for the unrelated control
cases. Do not edit files, run tests, make commits, or change the repository.

Your final response must contain only one valid JSON object, with no Markdown
fence and no explanatory text, matching this contract:

```json
{
  "schemaVersion": "fluxseer-engineering-baseline-results/v1",
  "runId": "run-N",
  "cases": [
    {
      "case_id": "exact corpus case id",
      "skill_activated": true,
      "decision": "PASS|NEEDS_CHANGES|BLOCKED",
      "identified": ["semantic_token"],
      "recommended": ["semantic_token"],
      "flags": [],
      "trace_ref": "run-N/exact-corpus-case-id"
    }
  ]
}
```

Requirements:

- Include exactly all 13 case IDs from the corpus, once each.
- Preserve the corpus case IDs exactly.
- Use only the decision values allowed by the contract.
- Use semantic tokens from the corpus expectations; do not invent prose in
  place of tokens.
- For `identified` and `recommended`, include every required expected token
  that the response supports.
- Put only explicitly forbidden semantic findings in `flags`; leave it empty
  when none are present.
- For violation cases, do not approve an unsafe or unsupported change.
- For insufficient RCA evidence, keep the result inconclusive rather than
  asserting an unsupported cause.
- For unrelated cases, set `skill_activated` to false and do not introduce
  engineering findings or recommendations.

The run number and `runId` are supplied in the user prompt for this invocation.
