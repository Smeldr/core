# A62: Shared template partials

## Background
Every Forge site currently duplicates nav and footer HTML across all module
templates (list and show). There is no mechanism to define a shared partial
once and include it across templates. This creates maintenance overhead and
divergence risk as sites grow.

## Why
Shared partials eliminate nav/footer duplication in forge-site and any other
Forge application. A developer should be able to define a partial once and
include it with a simple template call.

## Constraints
- Zero third-party dependencies.
- The solution must be opt-in — existing sites with no partials directory
  must continue to work unchanged.
- Partials must be available inside module templates (list.html, show.html)
  and home/base templates.
- The mechanism must be discoverable and consistent with existing Forge
  template conventions.
- Consider: where do partials live on disk? How are they registered? How
  does a developer include them in a template?
- All changes must be reflected in ARCHITECTURE.md and DECISIONS.md (A62).
- README.md and example_test.go must be updated if any documented example
  is affected.

## Your task
1. Analyse the existing template loading and parsing logic in templates.go
   and templatedata.go before proposing anything.
2. Plan your approach — directory convention, registration, include syntax —
   and present it for review before writing any code.
3. Wait for explicit approval before implementing.
