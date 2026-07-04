// triage-fixes — the HANDOFF + VERIFICATION half of the morning-triage loop.
//
// morning-triage (the skill) discovers findings and persists them to
// ./state/triage.md. This workflow picks up the open findings: one isolated
// worktree per finding for a generator agent, then an adversarial reviewer
// (same contract as examples/agents/reviewer.md) judges each draft, and only
// a PASS opens a DRAFT pull request — never a merge.
meta({
  name: 'triage-fixes',
  description: 'Draft fixes for open triage findings in isolated worktrees, adversarially review each, open draft PRs for passes',
  phases: [
    { title: 'Load', detail: 'read open findings from state/triage.md' },
    { title: 'Fix', detail: 'one worktree + generator agent per finding' },
    { title: 'Review', detail: 'adversarial reviewer judges each draft' },
    { title: 'Deliver', detail: 'draft PR per PASS; REJECTs go to inbox' },
  ],
})

const FINDINGS_SCHEMA = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          slug: { type: 'string' },
          finding: { type: 'string' },
          goal: { type: 'string' },
        },
        required: ['slug', 'finding', 'goal'],
      },
    },
  },
  required: ['findings'],
}

const VERDICT_SCHEMA = {
  type: 'object',
  properties: {
    verdict: { type: 'string', enum: ['PASS', 'REJECT'] },
    reasons: { type: 'array', items: { type: 'string' } },
  },
  required: ['verdict', 'reasons'],
}

phase('Load')
const loaded = await agent(
  'Read only ./state/triage.md; do not inspect source files, git history, CI, ' +
    'or the filesystem during this Load phase. Return every finding whose status is "open", as ' +
    '{slug, finding, goal} where slug is a short branch-safe name and goal is a ' +
    'verifiable stop condition for the fix (tests/lint that must pass). Return ' +
    '{"findings":[]} if none.',
  { label: 'load-findings', tools: ['read'], schema: FINDINGS_SCHEMA },
)
const findings = (loaded && loaded.findings) || []
log(findings.length + ' open finding(s)')

// Cap the batch explicitly: a human reviews these PRs, so never draft more
// than one person can actually read (Read-a-Sample discipline).
const batch = findings.slice(0, 3)
if (batch.length < findings.length) log('capped to ' + batch.length + ' (review bandwidth)')

let results = []
if (batch.length > 0) {
  results = await pipeline(
    batch,
    // HANDOFF: generator drafts the fix in its own worktree (physical isolation).
    (_, f) =>
      agent(
        'Draft a fix for this triage finding:\n' + f.finding + '\n\n' +
          'Definition of done: ' + f.goal + '\n' +
          'Work on a branch named fix/' + f.slug + '. Commit your changes. Do NOT ' +
          'merge, do NOT push to main, do NOT open a PR — a reviewer judges this first.',
        { label: 'fix:' + f.slug, phase: 'Fix', isolation: 'worktree' },
      ),
    // VERIFICATION: a fresh adversarial reviewer — the maker-checker door.
    // Same contract as examples/agents/reviewer.md: assume broken, execute
    // don't read, verdict with concrete reasons.
    (draft, f) =>
      agent(
        'ROLE: adversarial code reviewer (maker-checker). ASSUME the work is ' +
          'BROKEN until proven otherwise; do not praise.\n' +
          'A generator claims it fixed: ' + f.finding + '\non branch fix/' + f.slug +
          ', definition of done: ' + f.goal + '\nIts report:\n' + draft + '\n\n' +
          'CHECK by executing, not reading: check out the branch in a worktree, run ' +
          'the tests/lint named in the definition of done, probe edge cases the ' +
          'author skipped. PASS only if every check holds; REJECT needs concrete reasons.',
        {
          label: 'review:' + f.slug,
          phase: 'Review',
          tools: ['read', 'grep', 'ls', 'find', 'bash'],
          schema: VERDICT_SCHEMA,
        },
      ),
    // DELIVER: PASS → draft PR (the human door: never auto-merge).
    // REJECT → an inbox entry so the human sees it in the completion notice.
    (verdict, f) =>
      agent(
        verdict && verdict.verdict === 'PASS'
          ? 'Open a DRAFT pull request for branch fix/' + f.slug + ' non-interactively. ' +
            'First run `git push -u origin fix/' + f.slug + '`. Then run gh with an explicit base: ' +
            '`gh pr create --draft --base feat/loop --head fix/' + f.slug + ' --title "fix: ' + f.slug +
            '" --body "Automated triage fix. Definition of done: ' + f.goal + '"`. ' +
            'Capture the PR URL from gh output. Then update the finding row in ' +
            './state/triage.md to status pr-open and persist the draft PR URL in the ' +
            'same row so tomorrow can see exactly what is waiting for review. Then ' +
            'commit. NEVER merge it.'
          : 'The reviewer rejected the fix for ' + f.slug + ': ' +
            JSON.stringify(verdict && verdict.reasons) + '. Write ./inbox/' + f.slug +
            '.md summarizing the finding, the attempted fix, and the rejection ' +
            'reasons so a human can decide. Leave the finding open in ./state/triage.md.',
        { label: 'deliver:' + f.slug, phase: 'Deliver', tools: ['read', 'bash'] },
      ).then(() => ({ slug: f.slug, pass: !!(verdict && verdict.verdict === 'PASS') })),
  )
}

const done = results.filter(Boolean)
return {
  fixed: done.filter(r => r.pass).map(r => r.slug),
  rejected: done.filter(r => !r.pass).map(r => r.slug),
}
