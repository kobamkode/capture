# Commits

Every commit message in this repo uses Conventional Commits: `type(scope): summary`.

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`.

- `scope` is optional; use the module or area being changed.
- `summary` is imperative, lower-case, no trailing period: `fix(auth): stop refreshing expired tokens`.
- One logical change per commit. A breaking change gets `!` after the type or scope, plus a `BREAKING CHANGE:` footer.

This file governs the message. It does not authorize committing or pushing: do that only when the user or an orchestrator asks.
