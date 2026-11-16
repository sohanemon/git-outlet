# git-outlet

Publish selected files and folders from a private Git repository to public Gists and repositories. This lets you share parts of a project without making the source repository public.

## Install

On macOS, install the Homebrew Cask:

```sh
brew tap sohanemon/tap
brew install --cask git-outlet
```

The cask installs prebuilt macOS binaries. For Linux, Windows, or manual macOS installation, download the archive for your operating system and architecture from [GitHub Releases](https://github.com/sohanemon/git-outlet/releases), extract it, and put the executable (`git-outlet` or `git-outlet.exe` on Windows) on your `PATH`.

The release binaries are not code-signed or notarized. macOS Gatekeeper may block an unsigned binary from opening.

## Requirements

- `git` to read the private repository.
- GitHub CLI (`gh`) to publish. Sign in with `gh auth login` and configure Git authentication with `gh auth setup-git`.
- `gitleaks` is optional. It scans selected files and subtree history when installed. Without it, git-outlet warns and continues.
- `git subtree` is required for entries using subtree mode.

## Quick start

1. Create `outlet.yml` by copying `outlet.example.yml`. Set your GitHub owner and edit paths and names to match tracked files in your repository.
2. Check the configuration:

   ```sh
   git outlet check
   ```

3. Preview the publish:

   ```sh
   git outlet --dry-run
   ```

4. Publish:

   ```sh
   git outlet
   ```

## Config reference

The top-level `settings` block is optional. `publish` maps each public entry name to a path or entry mapping. A string value is shorthand for `path`.

### Settings

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `owner` | string | empty | GitHub owner; an empty value uses the authenticated `gh` account. |
| `mode` | string | `copy` | Default folder mode: `copy` or `subtree`. |
| `branch` | string | `main` | Destination branch for public repositories. |
| `message` | string | empty | Default copy-mode commit message; when empty, git-outlet uses `Update from private repo (<short HEAD>)`. |
| `secret_scan` | boolean | `true` | Run gitleaks before publishing and when `check --scan` is used. |

### Entry fields

The mapping key is the entry name. Use a file path for a Gist entry and a folder path for a repository entry.

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `path` | string | required | Tracked file or folder inside the private repository. |
| `description` | string | empty | Gist description or new public repository description. |
| `include` | list of strings | empty | Select matching tracked files in copy mode. |
| `exclude` | list of strings | empty | Remove matching tracked files after include selection; exclude wins. |
| `message` | string | empty | Copy-mode commit message; otherwise inherits `settings.message` and then the generated default. |
| `mode` | string | inherited from settings (`copy`) | Folder publishing mode. File entries always publish as Gists. |

Include and exclude patterns are relative to `path`. A pattern without `/` matches a base name at any depth. A pattern containing `/` matches the full path relative to `path`. `**` can match across directories. Only tracked files are considered. Subtree entries publish the whole folder and cannot use include or exclude patterns.

## Modes

| Mode | History | Commit message | Include/exclude | What becomes public |
| --- | --- | --- | --- | --- |
| `copy` | Only the current selected files are copied; private history is not copied. | `-m`, entry message, settings message, then a generated message. | Supported for folders. | Selected current files in a public repository; file entries are public Gists. |
| `subtree` | Publishes the full history of the folder, including deleted files and older versions. | Uses messages from private history; configured messages do not apply. | Not supported. | The folder and all of its history in a public repository. |

## Commands and flags

`git outlet check` validates the configuration and tracked paths. Add `--scan` to run gitleaks and report findings as errors. `settings.secret_scan: false` disables that scan; suspicious filename warnings remain enabled.

The root command publishes entries. Its flags are:

| Flag | Meaning |
| --- | --- |
| `--dry-run` | Scan and preview without creating repositories, pushing, publishing Gists, or writing state. |
| `-m`, `--message` | Override the copy-mode commit message. Subtree mode uses private history messages. |
| `--only NAME` | Publish only the named entry. |
| `--adopt` | Allow taking over an existing non-empty public repository that git-outlet does not have in its state file. |
| `--allow-suspicious` | Allow filenames that match the suspicious-file rules. Gitleaks findings still block publishing. |
| `--config PATH` | Read configuration from `PATH` instead of `outlet.yml`. |
| `--version` | Print the version, commit, and build date. |

Cobra provides shell completion:

```sh
source <(git-outlet completion bash)
git-outlet completion zsh
git-outlet completion fish
git-outlet completion powershell
```

## Safety

- git-outlet publishes tracked files only. Symlinks and missing tracked files are skipped.
- Folder destinations must be public repositories. Existing non-empty repositories not recorded in `.git-outlet-state.json` require `--adopt`.
- When enabled, gitleaks scans the exact files selected for copy mode and the history that subtree mode would publish. A finding blocks that entry, including during a dry run. Gitleaks is a detector, not a guarantee that every secret will be found.
- Suspicious filenames such as `.env`, private keys, and credential files are blocked independently of gitleaks. `--allow-suspicious` overrides only this filename rule.
- `.git-outlet-state.json` records Gist IDs, content hashes, and repository commits. Keep it with the private repository. If you lose the Gist IDs, git-outlet can create duplicate Gists.
- Subtree mode makes the folder's complete history public, including deleted files and older versions.

## Troubleshooting

- **GitHub push authentication fails:** run `gh auth login`, then `gh auth setup-git` so Git can use the GitHub CLI credentials.
- **`git subtree` is unavailable:** install the `git-subtree` package or enable the subtree command provided by your Git distribution.
- **A subtree push is rejected:** the public repository may have changed outside git-outlet. Review both histories and reconcile them before retrying; git-outlet does not force-push.
- **A Gist was deleted on GitHub:** remove that entry from `.git-outlet-state.json` and publish again to create a new Gist.
- **Copy-mode commit fails with a missing identity:** configure `git config user.name` and `git config user.email` in the private repository.

## Development

Run the checks and build locally:

```sh
go test ./...
go vet ./...
go build -o git-outlet ./cmd/git-outlet
```

To cut a release, create and push a version tag. GitHub Actions builds and publishes the release:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

## License

MIT. See [LICENSE](LICENSE).
