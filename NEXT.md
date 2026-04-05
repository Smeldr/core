# Next task for corepilot

## Task: Add GitHub Actions CI workflow

### What

Create `.github/workflows/ci.yml` in the forge repo root.

### Why

forge-pgx already has an integration test (`pgx_integration_test.go`) with a
`//go:build integration` guard and a `DATABASE_URL` skip condition. It has never
been run against a real database. This workflow provides the infrastructure to
run it automatically on every push and pull request, with no manual setup required.

### Scope

One new file only: `.github/workflows/ci.yml`
No changes to existing files.

### The workflow file

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    name: Test (unit)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run unit tests
        run: go test ./...

  integration:
    name: Test (forge-pgx integration)
    runs-on: ubuntu-latest

    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: forge
          POSTGRES_PASSWORD: forge
          POSTGRES_DB: forgetest
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 5s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run forge-pgx integration tests
        working-directory: forge-pgx
        env:
          DATABASE_URL: postgres://forge:forge@localhost:5432/forgetest
        run: go test -v -tags integration ./...
```

### Constraints

- Commit the file exactly as shown — no modifications.
- This is not an amendment — no DECISIONS.md entry needed. It is a CI
  infrastructure addition with no forge API surface changes.
- After committing, delete this NEXT.md file and update context/corepilot.md
  with the new HEAD SHA and a note that CI is now in place.
- Run `go test ./...` locally before committing to confirm unit tests still pass.
