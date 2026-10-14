# How we work together

These rules keep `main` always working and make it easy to see who changed what and why.

---

## The workflow, step by step

**1. Pick or create an issue** on GitHub describing the work. Assign it to yourself.

**2. Update your copy of `main`:**

```bash
git switch main
git pull
```

**3. Create a branch** for the work. Never commit straight to `main`.

```bash
git switch -c feat/plan-api
```

| Prefix | Use for | Example |
|---|---|---|
| `feat/` | New feature | `feat/mpesa-stk-push` |
| `fix/` | Bug fix | `fix/radius-ipv6-start` |
| `docs/` | Documentation only | `docs/mikrotik-guide` |
| `chore/` | Tooling, CI, dependencies | `chore/ci-cache` |
| `refactor/` | Code change, same behaviour | `refactor/config-loader` |

**4. Make small commits** with clear messages ([Conventional Commits](https://www.conventionalcommits.org/) style):

```
feat(plan): add create-plan endpoint
fix(radius): send Session-Timeout in seconds
docs(setup): explain mirrored networking
```

Format: `type(area): what changed`. Write it as an instruction, in under 72 characters.

**5. Check everything before pushing:**

```bash
make check                                   # Go: format, vet, tests
make radius-config && make radius-test       # only if you changed radius/ or scripts/
```

**6. Push and open a pull request:**

```bash
git push -u origin feat/plan-api
```

GitHub prints a link. Open it, fill in the template, and request a review.

**7. Wait for green CI and one approval**, then use **"Squash and merge"**. That turns your branch into one tidy commit on `main`.

**8. Clean up:**

```bash
git switch main && git pull
git branch -d feat/plan-api
```

---

## Code rules

- **Go:** code must pass `gofmt` and `go vet`. Run `make fmt` before committing.
- **New feature package** → goes in `internal/<feature>/`, with tests in the same folder (`*_test.go`).
- **Every database table we own has a `tenant_id`** (from Phase 1). Every query filters by it. This keeps owners' data separate.
- **Database changes** → a new file in `migrations/`. Never change a migration that's already on `main`; add a new one.
- **FreeRADIUS changes** → edit the template in `radius/`, never `/etc/freeradius` directly.
- **Shell scripts** → must pass `shellcheck` (CI checks).

## Secrets

- Never commit `.env`, passwords, API keys, M-Pesa credentials or RADIUS secrets.
- New setting? Add it to `.env.example` with a placeholder value and a comment.
- Accidentally committed a secret? Tell the team immediately and **change the secret**. Deleting the commit is not enough, because Git history keeps it.

---

## Protecting `main`

Do this once, as repository admin, after the first push:

1. GitHub → repo → **Settings → Branches → Add branch ruleset** (or "Add rule").
2. Target branch: `main`.
3. Turn on:
   - **Require a pull request before merging** → required approvals: **1**
   - **Require status checks to pass** → add all three CI jobs:
     `Go — format, vet, test, build`, `Shell scripts — shellcheck`, `FreeRADIUS — install, configure, test`
   - **Block force pushes**
4. Save.

The CI jobs only appear in the status-check list after CI has run at least once.

## Adding a collaborator

GitHub → repo → **Settings → Collaborators → Add people**. They then follow
[docs/setup/01-wsl-freeradius.md](docs/setup/01-wsl-freeradius.md), Section 3B.

## Recording big decisions

When the team makes a decision that's hard to reverse (a library, a protocol, a hosting choice), add a short file in `docs/adr/`. Copy the format of an existing one.
