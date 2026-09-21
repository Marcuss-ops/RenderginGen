# Drive credentials — single place

**Canonical host location:** `~/.config/velox/credentials.json` and `~/.config/velox/token.json`
(0600, owned by the operator). This path is the only one on disk — `drive-upload`
defaults there and the container bind-mounts it.

**Never in the repo:** `credentials.json` / `token.json` are covered by the top-level
`.gitignore` (`credentials.json`, `token.json`). No copy lives under `RenderingGen/`.

**Docker usage:**

```sh
# Host -> container bind mount (docker-compose or docker run):
#   - ~/.config/velox/credentials.json:/etc/renderinggen/credentials.json:ro
#   - ~/.config/velox/token.json:/etc/renderinggen/token.json:ro

# Direct upload helper:
RenderingGen/bin/drive-upload \
  -credentials ~/.config/velox/credentials.json \
  -token ~/.config/velox/token.json \
  -folder <drive-folder-id> -file <artifact>
```

If you previously had working copies under `infra/docker/`, remove them:

```sh
rm -f RenderingGen/infra/docker/credentials.json RenderingGen/infra/docker/token.json
```

**Verify history contains no secrets:**

```sh
git -C RenderingGen log --all --diff-filter=A -- infra/docker/credentials.json infra/docker/token.json
# (no output: never tracked)
git -C RenderingGen ls-files | grep -E 'credentials|token\.json'  # (no output)
```
