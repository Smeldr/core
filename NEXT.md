# Task: Switch plan file delivery to local write

## Why

Both pilots now run locally and have direct filesystem access to all three
repos. Using GitHub MCP to write and delete plan files adds unnecessary
latency and is a potential source of sync drift. The sitepilot has already
switched to local writes. Corepilot should do the same.

## What

Update your copilot-instructions (or wherever your sprint workflow is
documented) to reflect that plan files are written and deleted locally,
not via GitHub MCP.

The new workflow:

- After reading NEXT.md, write your plan file directly to:
  C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md
  (local file write — no GitHub MCP)

- After sprint is committed, delete the plan file locally:
  Remove-Item "C:\Users\peter\Documents\Code\forge-architect\plans\core-next-plan.md"
  (local delete — no GitHub MCP)

Everything else is unchanged: read NEXT.md locally, delete it locally after
commit, update context/corepilot.md via GitHub MCP as before.

## Definition of done

- copilot-instructions updated with correct local plan file paths
- No other behaviour changed
- No plan file needed for this task — just update and commit
