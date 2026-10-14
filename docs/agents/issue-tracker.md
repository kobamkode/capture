# Issue tracker: Beads

Issues and specs for this repo live in the beads database. Use the `bd` CLI for all operations; flag details are in `bd <command> --help`, don't memorize them.

## Conventions

- **Spec**: one issue, `--type feature`, full spec body as description. Its id is the spec's identity for every later reference.
- **Ticket**: one issue per ticket, `--type task`, parented to the spec (`--parent <spec-id>`): what-to-build as description, acceptance criteria via `--acceptance`, priority `-p 0-4`.
- **Blocking edges are native**: wire them at publish time with `bd dep add <blocked-id> <blocker-id>` (or `--deps 'blocked-by:id'` on create), in dependency order. Never restate an edge in prose when the dependency exists.
- **Triage roles are labels**: `bd tag <id> <label>` / `bd label add` (role strings live in `triage-labels.md`).
- **Work state**: `bd update <id> --status <open|in_progress|blocked|deferred|closed>`. `bd ready` is the frontier (open, unblocked, unclaimed); `bd blocked` lists stuck tickets.

## When a skill says "publish to the issue tracker"

Create the issue with `bd create` as above. For a batch of tickets: one `bd create` per ticket in dependency order (blockers first), or one shot from a graph file (`bd create --graph plan.json`, `bd create --help` for the schema). Apply the `ready-for-agent` label before handing off.

## When a skill says "fetch the relevant ticket"

`bd show <id>` for the full body, criteria, and deps; `bd search <text>` when only a title fragment is known.

## Wayfinding operations

Used by `/wayfinder`:

- **Map**: one issue with `--type epic`; append decisions as `bd note <map-id> "<text>"` and keep the description as the index (Destination / Notes / Decisions-so-far / Not yet specified / Out of scope).
- **Child ticket**: `--parent <map-id>`.
- **Blocking**: `bd dep add` native edges.
- **Frontier**: `bd ready --json`, unblocked and unclaimed.
- **Claim**: `bd ready --claim --json` (atomic, lease-backed) or `bd update <id> --status in_progress` when the id is already known.
- **Resolve**: post the answer with `bd comment <id> --file answer.md`, then `bd close <id>`; append the one-line gist + id to the map's Decisions-so-far.

## Consuming skills

`/implement-spec-workmux` drives this tracker directly: claims via `bd ready --claim`, merges each worker branch, closes only after the merge lands.
