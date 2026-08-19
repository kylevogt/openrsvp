# Agent instructions

## Running the app locally

To see a change in a browser — not just in tests — use `scripts/dev-server.sh`:

```bash
make demo                               # build, start on :8099, seed demo events
./scripts/dev-server.sh --seed          # the same thing
./scripts/dev-server.sh --seed --fresh  # ...from an empty database
node scripts/seed-demo.mjs              # re-seed a server that is already up
```

It prints the URLs to open and blocks until Ctrl-C. State lives in `.dev/`
(gitignored), so data survives a restart; the log is `.dev/server.log`. Pass
`DEV_SKIP_BUILD=1` to reuse `./bin/openrsvp` when only Go code changed — note
that `make build` compiles the frontend into the binary, so a web change needs a
rebuild to show up.

Things that are easy to get wrong:

- **`make dev` is not this.** It runs `go run` without the embedded frontend, so
  every page is the bare SPA fallback. Use `dev-server.sh` for anything visual.
- **`ENV=development` is required to log in.** There is no mail provider
  locally, so magic-link tokens are only obtainable from the server log — the
  server writes them only in development mode. `scripts/seed-demo.mjs` reads
  them from the log to authenticate; do the same rather than inventing a
  backdoor.
- **The public RSVP endpoint allows 30 requests/minute per IP.** Seeding many
  guests means handling `429` and waiting the window out.

`scripts/seed-demo.mjs` creates one event per guest-list visibility setting
(headcount, guest list, both) with a spread of plus-ones and a declined guest,
which is usually the fastest way to eyeball a change to the public pages. Add
`--big` for a 55-guest event that exercises the guest list's overflow toggle.
Extend the seeder when a change needs a state it does not cover yet.

For driving the app programmatically, `tests/e2e/` already has Playwright and
the browser wired up — `cd tests/e2e && npm ci && npx playwright install
chromium`, then point a script at `E2E_BASE_URL`. Prefer adding a spec there
over writing a throwaway driver.

## Version bumps

Every PR we create that ships product changes must bump `VERSION` and add a matching changelog entry at the top of the Changelog section in `README.md`. Do this in the same PR as the work, not a follow-up.

Merging a `VERSION` bump to `main` tags `vX.Y.Z` and publishes the Docker image (`ghcr.io/kylevogt/openrsvp`) once CI is green. If `VERSION` is unchanged, the change will not ship.

- **Patch** (`x.y.Z`): fixes, visual tweaks, small behavior changes
- **Minor** (`x.Y.0`): new user-facing features or schema migrations
- **Major** (`X.0.0`): breaking changes

Write changelog entries in the same voice as existing ones: lead with the user-visible problem, then the fix. Date them the day the PR is opened.
