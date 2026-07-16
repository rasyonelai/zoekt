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

Published to **`ghcr.io/rasyonelai/zoekt`** (not project-specific names).

```bash
docker pull ghcr.io/rasyonelai/zoekt:main
```

Pin by digest or semver tag in production — avoid floating `main` in customer deployments.

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
