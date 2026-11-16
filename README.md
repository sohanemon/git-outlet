# git-outlet

Publish selected files and folders from any local Git repository as public Gists or repositories. Use it to share part of a private project without publishing the whole source.

## Install

With Homebrew on macOS:

```sh
brew install --cask sohanemon/tap/git-outlet
```

With [mise](https://mise.jdx.dev/):

```sh
mise use -g github:sohanemon/git-outlet
```

Or download an archive for Linux, Windows, or macOS from [GitHub Releases](https://github.com/sohanemon/git-outlet/releases) and put `git-outlet` (or `git-outlet.exe`) on your `PATH`.

## Quick start

1. Copy [outlet.example.yml](outlet.example.yml) to `outlet.yml` and edit the paths and names.
2. Check and preview:

   ```sh
   git outlet check
   git outlet --dry-run
   ```

3. Publish:

   ```sh
   git outlet
   ```

## Example

```yaml
settings:
  owner: your-github-name

publish:
  parser-lib: packages/parser
  readme:
    path: docs/README.md
    description: Parser library guide
```

File entries publish as Gists. Folder entries publish as public repositories in `copy` mode by default.

<details>
<summary>Configuration</summary>

`publish` maps public names to tracked files or folders. A string is shorthand for an entry's `path`.

### Settings

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `owner` | string | empty | GitHub owner; empty uses the authenticated `gh` account. |
| `mode` | string | `copy` | Default folder mode: `copy` or `subtree`. |
| `branch` | string | `main` | Branch for public repositories. |
| `message` | string | empty | Default commit message for copy-mode folders. |
| `secret_scan` | boolean | `true` | Run gitleaks before publishing and when `check --scan` is used. |

### Entry fields

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `path` | string | required | Tracked file for a Gist or folder for a repository. |
| `description` | string | empty | Gist or newly created repository description. |
| `include` | list of strings | empty | Include matching files in copy mode. |
| `exclude` | list of strings | empty | Exclude matching files after inclusion; exclude wins. |
| `message` | string | empty | Copy-mode commit message; overrides the settings message. |
| `mode` | string | inherited from settings (`copy`) | Folder publishing mode. File entries always publish as Gists. |

Patterns are relative to `path`. A pattern without `/` matches a base name at any depth; a pattern with `/` matches the full relative path. `**` matches across directories. Only tracked files are selected. `subtree` publishes the whole folder and does not support include or exclude patterns.
</details>

<details>
<summary>Modes and safety</summary>

| Mode | History published | Message | Selection |
| --- | --- | --- | --- |
| `copy` | Current selected files only | `-m`, entry message, settings message, then a generated message | Include/exclude supported |
| `subtree` | Full folder history, including deleted files and old versions | Source commit messages | Whole folder |

Folder repositories must be public. An existing non-empty repository that git-outlet does not record requires `--adopt`.

Before publishing, git-outlet blocks suspicious filenames such as `.env`, private keys, and credential files. If gitleaks is installed and `secret_scan` is enabled, it also scans the selected copy files or the history that subtree mode would publish. Findings block that entry. Gitleaks may not find every secret. `--allow-suspicious` bypasses only the filename rule.

`.git-outlet-state.json` stores Gist IDs and content hashes, plus repository commits. Keep it with the source repository; losing Gist IDs can cause duplicate Gists. Subtree mode exposes the folder's complete history.
</details>

<details>
<summary>Commands</summary>

| Command or flag | Purpose |
| --- | --- |
| `git outlet check` | Validate configuration and selected tracked paths. |
| `git outlet check --scan` | Also scan with gitleaks and report findings as errors. |
| `git outlet --dry-run` | Scan and preview without publishing or writing state. |
| `-m, --message TEXT` | Override the copy-mode message; ignored by subtree mode. |
| `--only NAME` | Publish only one entry. |
| `--adopt` | Allow using an existing public repository not recorded in state. |
| `--allow-suspicious` | Publish suspiciously named files, while still blocking gitleaks findings. |
| `--config PATH` | Read a different config file. |
| `--version` | Print version, commit, and build date. |

`-m` applies to every copy-mode folder in the run and overrides entry and settings messages. Gists and subtree entries ignore it. Empty or whitespace-only messages are rejected. During a dry run, changed folders show the message that would be committed; multiline messages show only the first line.

Run the command for your shell. Add it to your shell's startup file to enable completion in future sessions.

### Bash

```sh
source <(git-outlet completion bash)
```

### Zsh

```sh
source <(git-outlet completion zsh)
```

### Fish

```fish
git-outlet completion fish | source
```
</details>

<details>
<summary>Requirements and troubleshooting</summary>

- `git` and `gh`.
- Publishing requires a logged-in GitHub CLI account. Run `gh auth login`; run `gh auth setup-git` if Git push authentication is not configured.
- `gitleaks` is optional. If missing, git-outlet warns and continues.
- `git subtree` is required for subtree mode.

**Push authentication fails:** run `gh auth setup-git`.

**`git subtree` is missing:** install the `git-subtree` package or enable the command provided by your Git distribution.

**A subtree push is rejected:** the public repository may have changed outside git-outlet. Reconcile the histories before retrying; pushes are never forced.

**A Gist was deleted:** remove its entry from `.git-outlet-state.json` and publish again.

**A copy-mode commit has no identity:** configure `git config user.name` and `git config user.email` in the source repository.
</details>

<details>
<summary>Development and releases</summary>

Requires Go 1.22 or newer.

```sh
go test ./...
go vet ./...
go build -o git-outlet ./cmd/git-outlet
```

Create and push a version tag to publish a release:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```
</details>

## License

MIT. See [LICENSE](LICENSE).
