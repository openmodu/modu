---
id: modu_code_sanity
name: modu_code sanity
category: productivity
grading_type: deterministic
timeout_seconds: 60
workspace_files: []
checks:
  - name: assistant responded
    type: assistant_responded
  - name: mentions ready
    type: output_regex
    pattern: "(?i)ready|ok|done|可以|完成"
---

## Prompt

Reply with a short confirmation that you are ready to work in this repository.

## Expected Behavior

The agent should produce a non-empty confirmation response.
