# Agent instructions

## Version bumps

Every PR we create that ships product changes must bump `VERSION` and add a matching changelog entry at the top of the Changelog section in `README.md`. Do this in the same PR as the work, not a follow-up.

Merging a `VERSION` bump to `main` tags `vX.Y.Z` and publishes the Docker image (`ghcr.io/kylevogt/openrsvp`) once CI is green. If `VERSION` is unchanged, the change will not ship.

- **Patch** (`x.y.Z`): fixes, visual tweaks, small behavior changes
- **Minor** (`x.Y.0`): new user-facing features or schema migrations
- **Major** (`X.0.0`): breaking changes

Write changelog entries in the same voice as existing ones: lead with the user-visible problem, then the fix. Date them the day the PR is opened.
