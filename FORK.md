# Rasyonel AI Zoekt Fork

Fork of [sourcegraph/zoekt](https://github.com/sourcegraph/zoekt) with additional mirror tools for enterprise code hosts.

## Added mirror tools

| Tool | Config fields | Notes |
|------|---------------|-------|
| `zoekt-mirror-ado` | `AzureDevOpsURL`, `AzureDevOpsOrg`, `AzureDevOpsOrgs`, `AzureDevOpsProjects`, `AzureDevOpsRepos`, `AzureDevOpsUseTfsPath` | Azure DevOps Cloud + Server |
| `zoekt-mirror-bitbucket-cloud` | `BitBucketCloud`, `BitBucketCloudWorkspace`, `BitBucketCloudWorkspaces`, `BitBucketCloudProjects`, `BitBucketCloudRepos` | Bitbucket Cloud API v2 |

ADO discovery follows [Sourcebot azuredevops.ts](https://github.com/sourcebot-dev/sourcebot/blob/main/packages/backend/src/azuredevops.ts).  
Bitbucket Cloud discovery follows [Sourcebot bitbucket.ts](https://github.com/sourcebot-dev/sourcebot/blob/main/packages/backend/src/bitbucket.ts).

## Container image

Published to **`ghcr.io/rasyonelai/zoekt`** on release only (not on every `main` push).

```bash
docker pull ghcr.io/rasyonelai/zoekt:v2025.07.20
# or after a semver release:
docker pull ghcr.io/rasyonelai/zoekt:v2025.07
```

**Always pin by commit SHA or semver tag** in Atlas (`ZOEKT_IMAGE_TAG` in `.env`). Do not use floating tags in production.

### Release workflow (upstream sync)

Local clones often have `origin` → `sourcegraph/zoekt` (upstream) and **`rasyonelai`** → `rasyonelai/zoekt` (fork). Push fork changes to **`rasyonelai`**, not `origin`.

1. Rebase onto `sourcegraph/zoekt` main and fix conflicts in mirror tools if needed.
2. `git push rasyonelai main`
3. Run local smoke (index + search) or rely on Atlas `pnpm docker:code-smoke`.
4. Publish image — either:
   - **Tag release:** `git tag v2025.07.21 && git push rasyonelai v2025.07.21` (CI builds and pushes to GHCR), or
   - **Manual:** Actions → Docker → Run workflow on the target commit.
5. In Atlas: set `ZOEKT_IMAGE_TAG` to the new tag or short SHA in `.env` / `.env.example` and `docker-compose.yml` defaults.
6. Redeploy: `docker compose pull zoekt-webserver && docker compose build zoekt-indexserver code-mirror && docker compose up -d zoekt-webserver zoekt-indexserver code-mirror`

CI triggers: `workflow_dispatch` and `v*` tags only — ordinary merges to `main` do not rebuild the image.

## mirror.json examples

### Azure DevOps Cloud

```json
{
  "AzureDevOpsURL": "https://dev.azure.com",
  "AzureDevOpsOrg": "my-org",
  "CredentialPath": "/run/secrets/ado-pat",
  "Name": ".*"
}
```

### Azure DevOps Server

```json
{
  "AzureDevOpsURL": "https://ado.example.com",
  "AzureDevOpsOrg": "DefaultCollection",
  "AzureDevOpsUseTfsPath": true,
  "CredentialPath": "/run/secrets/ado-pat"
}
```

### Bitbucket Cloud

```json
{
  "BitBucketCloud": true,
  "BitBucketCloudWorkspace": "my-workspace",
  "CredentialPath": "/run/secrets/bb-cloud-token",
  "BitBucketCloudNoForks": true
}
```

## Upstream sync

Rebase periodically onto `sourcegraph/zoekt` main. Mirror tool additions live under `cmd/zoekt-mirror-ado` and `cmd/zoekt-mirror-bitbucket-cloud`; indexserver wiring is in `cmd/zoekt-indexserver/config.go`.

See **Release workflow** under Container image above when publishing a new GHCR image and bumping Atlas.
