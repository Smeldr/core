# Smeldr

**Keeps decisions coherent when people and AI agents move fast.**

A searchable graph of your decisions: who decided, what is in force, and what builds on what. Kept apart from the context the reasoning came from.

Governance is set per domain and area: a domain's people ratify its decisions, an area's people approve changes to its files. Built for coding. Enforceable as merge gates when you want it.

Self-hosted. Open source. No runtime dependencies.

[![Go Reference](https://pkg.go.dev/badge/smeldr.dev/core.svg)](https://pkg.go.dev/smeldr.dev/core)
[![codecov](https://codecov.io/gh/Smeldr/core/graph/badge.svg)](https://codecov.io/gh/Smeldr/core)
[![Version](https://img.shields.io/github/v/release/smeldr/core?filter=v*&color=%2341A300)](https://github.com/smeldr/core/releases/latest)

- **Decide.** Propose a Decision. Someone with the right ratifies it. It is never edited, only replaced, and the old one stays on record.
- **Know.** Every action records who did it: a person, an AI agent or a job. Ask what is in force and what rests on it.
- **Enforce.** Tie a Decision to the files it governs. A pull request shows the Decisions that apply and waits for the answer.

**v1.182.0: stable.** Public APIs are stable within v1.
See [CHANGELOG.md](CHANGELOG.md).

## Start in three minutes

**1. Run the server.**

Download the server for your system, check it against the release's checksums, and start it:

    curl -LO https://github.com/Smeldr/core/releases/download/v1.180.0/smeldr-server-linux-amd64
    curl -sL https://github.com/Smeldr/core/releases/download/v1.180.0/SHA256SUMS | grep smeldr-server-linux-amd64 | sha256sum -c
    mv smeldr-server-linux-amd64 smeldr-server && chmod +x smeldr-server
    SECRET=$(openssl rand -hex 32) BASE_URL=http://localhost:8080 \
      ENABLE_TOKENS=1 ENABLE_GOVERNANCE=1 ENABLE_RELATIONS=1 ENABLE_GOVERNANCE_RECORDS=1 \
      ./smeldr-server

The check prints `smeldr-server-linux-amd64: OK`. On another system, use the matching file from the release instead of `linux-amd64`:

- **Linux on ARM:** `linux-arm64`.
- **macOS:** `darwin-arm64` (Apple silicon) or `darwin-amd64`. Check with `shasum -a 256 -c`. A file downloaded in a browser is blocked by Gatekeeper until you run `xattr -d com.apple.quarantine <file>`.
- **Windows:** use Git Bash and the `windows-amd64.exe` files, renamed to `smeldr-server.exe` and `smeldr-cli.exe`. Git Bash still runs them as `./smeldr-server` and `./smeldr-cli`. In WSL, use the `linux-amd64` files as shown.

We have run these steps end to end on Linux. On macOS and Windows they have not been run end to end yet.

You should see the bootstrap admin token, shown once, and then the ready line:

    WARN smeldr: smeldr_tokens is empty ... bootstrap admin token created (copy now, shown once):
    <TOKEN>
    smeldr-server: listening on 127.0.0.1:8080

Leave it running.

**2. Make a token for yourself, and one for your AI agent.**

In a second terminal, in the same directory, download the CLI the same way:

    curl -LO https://github.com/Smeldr/cli/releases/download/v0.35.1/smeldr-cli-linux-amd64
    curl -sL https://github.com/Smeldr/cli/releases/download/v0.35.1/SHA256SUMS | grep smeldr-cli-linux-amd64 | sha256sum -c
    mv smeldr-cli-linux-amd64 smeldr-cli && chmod +x smeldr-cli

With Go 1.26.5 or later you can instead run `go install smeldr.dev/cli/cmd/smeldr-cli@v0.35.1` and drop the `./` below.

Then, with the bootstrap token from step 1:

    SMELDR_BOOTSTRAP_TOKEN=<TOKEN> ./smeldr-cli init --url http://localhost:8080 --name <you>

You should see:

    OK  connected to http://localhost:8080
    OK  token "<you>" created (class human, admin, 365 days)
    OK  grants: admin, approver (founder of the organization, D142): this token can ratify Decisions
    OK  .smeldr-cli.env written (mode 600; keep it out of version control: add it to .gitignore)
    OK  verification passed: the new token acts as admin

That token is yours, and it can ratify. Your AI agent gets its own, with less:

    ./smeldr-cli token create my-agent editor 30 --class agent --reason "my agent"
    ./smeldr-cli grant <token_id> editor --reason "my agent"

Point your agent at `http://127.0.0.1:8080/mcp` and give it that token as a Bearer token.

**3. Ask it to propose a Decision.**

> Propose a Decision: "We never log session tokens."

You should see: a Decision in state `proposed`.

**4. Try to ratify it as the agent.**

A Decision has to be placed before it can be ratified. Ask for both in one go:

> Place that Decision in the organization, then ratify it.

The agent places it (Decision D1 to the organization ORG1, `belongs_to_organization`), then tries to ratify and is refused:

    {"code": -32001, "message": "forbidden: an agent token never ratifies, supersedes or amends a Decision (D142); a human approver does"}

That is the model at work: an AI agent proposes, a person decides. Now you ratify it:

    ./smeldr-cli transition Decision D1 --to ratified --reason "We never log session tokens"

D1 is now `ratified`, it holds, and it no longer awaits anyone.

**5. Ask what applies to a file.**

> Which Decisions apply to `internal/billing/handler.go` in `acme/app`?

The prompt must name a repository. On a fresh server the answer lists the path as uncovered, even for an organization-wide Decision, because no Area covers it:

    {"repository":"acme/app","areas":[],"decisions":[],"uncovered":["internal/billing/handler.go"]}

After one Domain, one Area and one coverage rule (four calls):

    {"repository":"acme/app","areas":[{"address":"ARE1","name":"Billing","approval_mode":"comment-only","domain":"DOM2","paths":["internal/billing/handler.go"],"rules":["COV1"]}],"decisions":[{"address":"D1","title":"We never log session tokens","state":"ratified","standing":"holds","place":"organization","place_address":"ORG1"}],"uncovered":[]}

That is the model. **Next: [gate your pull requests](https://smeldr.dev/docs/gated-merges-self-hosted)**, with a GitHub App and a required status check.

## What Smeldr gives you

**Decisions that stay put**
- Propose, ratify, replace. A ratified Decision is never edited; a new one supersedes it and both stay.
- Re-evaluate on a date, so nothing silently goes stale (needs `ENABLE_EVAL_QUEUE_DRAIN`).
- A Decision is in force (`holds`), ceased or none, recorded on the item. Who started and who ended it is in the history (needs `ENABLE_PROVENANCE`).
- Two detectors flag an inconsistency: a Decision that contradicts another, and a Decision resting on one no longer in force. They flag, they do not prevent.

**Places and roles**
- Domains and Areas you name yourself. A domain's people ratify its Decisions; an area's people approve changes to its files.
- An AI agent can approve changes in one Area when a person has given it that right, for as long as the person says. An agent never ratifies.

**A record you can ask**
- Search across every record you may read.
- Every write records who did it and what kind of actor it was: a person, an AI agent or a job (the bootstrap token and older tokens are unclassified). The full history and the reason, when one is given, need `ENABLE_PROVENANCE`.
- Every item says whether it awaits action.

**Enforcement in code**
- Ask which Decisions apply to a list of paths in a repository (needs an Area with a coverage rule).
- Pull request checks (`ENABLE_GITHUB`, core v1.175.0 or later). In an Area set to `requires-approval`, with the check required in GitHub, a merge waits until the approver answers; the default `comment-only` does not wait. Smeldr does not read the code against the text of a Decision: the approver does.

**The same model runs content**
- Content types, publishing and scheduling, JSON, HTML and Markdown from one endpoint, sitemap, RSS, JSON-LD, AI indexing, and the same lifecycle and roles. See [Use Smeldr as a Go library](docs/CONTENT.md).

## What a Decision looks like

A real Decision from this repo's own history (D59, excerpted for length):

```json
{
  "decision_number": "D59",
  "state": "ratified",
  "body": "Goals are places: new `contains` RelationStore kind (working name) gives Smeldr a first-class, user-defined taxonomy... Any item relates to a Goal via the new kind; the taxonomy is user/org-defined since Goals are already freely created, not a fixed enum.",
  "next_eval_at": null
}
```

Smeldr's own architecture decisions are tracked as `Decision` items and ratified the same way any Smeldr-hosted decision is. Full text: [DECISIONS.md](DECISIONS.md).

## More examples

`example/server` is a deployable binary with no hard-coded Go content types; define everything at runtime via MCP:

    cd example/server
    SECRET=$(openssl rand -hex 32) BASE_URL=http://localhost:8080 go run .

Optional subsystems are gated by environment variables (`ENABLE_TOKENS`,
`ENABLE_GOVERNANCE`, `ENABLE_RELATIONS`, `ENABLE_DYNAMIC_CONTENT`, `ENABLE_BLOCKS`,
`ENABLE_MEDIA`, `ENABLE_SOCIAL`, `ENABLE_WEBHOOKS`, `ENABLE_PROVENANCE`,
`ENABLE_STRUCTURAL_SWEEP`, `ENABLE_AGENTS`, `ENABLE_GITHUB`, `OAUTH_ISSUER`).
`ENABLE_GITHUB` turns on gated merges for a GitHub repository with environment
variables only (see `docs/SELF_HOSTING.md`).
Set any to a non-empty value to enable that subsystem. See `example/server/main.go`
for the full variable reference and wiring order.

## Reference

Content types and the Go library: [CONTENT.md](docs/CONTENT.md)  
Full API reference: [REFERENCE.md](docs/REFERENCE.md)  
Self-hosting (release binaries, config, backup/restore): [SELF_HOSTING.md](docs/SELF_HOSTING.md)  
Web docs: [smeldr.dev/docs](https://smeldr.dev/docs)

For full token management reference (create, list, revoke) see [REFERENCE.md: Token management](docs/REFERENCE.md#token-management).  
For draft preview tokens see [REFERENCE.md: Draft preview](docs/REFERENCE.md#draft-preview).

---

## License

[AGPL v3](LICENSE): free for individuals, open source projects, and companies
building their own sites. A commercial license will be available for organisations
running Smeldr as a hosted service. See [COMMERCIAL.md](COMMERCIAL.md).
