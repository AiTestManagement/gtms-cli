# Code Signing Policy

This policy documents how GTMS Windows binaries are signed, who reviews and approves each signing operation, and how users can verify a signed binary.

## Signing authority

GTMS uses [SignPath Foundation](https://signpath.org/) for Authenticode code signing under their free programme for open-source projects. Signing is performed through SignPath's managed service; no signing keys are held by the GTMS project or by any individual maintainer.

## Artefacts covered

- Windows binaries (`gtms.exe` for `windows/amd64` and `windows/arm64`) attached to GitHub Releases.
- Windows installers (MSI / winget packages) when those distribution channels go live.
- macOS and Linux binaries are not Authenticode-signed — Authenticode applies to Windows only.

Signing applies to the first release after SignPath Foundation approval and to all subsequent releases. Earlier releases (v0.2.0 and prior) are unsigned.

## Roles and approvals

GTMS is currently a solo-maintained project. All SignPath signing roles are held by the project maintainer team:

- **Author** — the GTMS maintainer — produces the release artefact.
- **Reviewer** — the GTMS maintainer — manually reviews the built artefact before requesting signing.
- **Approver** — the GTMS maintainer — approves the signing request in SignPath's portal before the signature is applied.

**Every signing request is manually approved.** Automated or unattended signing is not used. If additional maintainers join the project, this document will be updated to distribute the roles across maintainers rather than collapsing them.

## Build provenance

- Release binaries are built in this public repository on GitHub-hosted runners via `.github/workflows/release.yml`, triggered by a `v*` tag push.
- Every signed binary is traceable from the release tag to the corresponding workflow run and source commit.
- SignPath's GitHub Action submits the built artefact for signing after the build step and before upload to the GitHub Release.

## Verifying a signed binary

On Windows, using `signtool` from the Windows SDK:

```
signtool verify /pa /v gtms.exe
```

Expected: exit code 0, a valid Authenticode signature chain with SignPath Foundation as the signer, and a valid timestamp. If verification fails unexpectedly on a binary downloaded from an official GitHub Release, please report it via the channels below.

## Reporting a signing concern

If you observe a GTMS binary that appears to have been signed outside this policy, or if you believe the signing credentials have been misused, please report it through either of the following:

- **Email** — <contact@testmanagement.com>
- **GitHub Security Advisories** — https://github.com/aitestmanagement/gtms-cli/security/advisories (private disclosure via GitHub)

SignPath Foundation's portal allows immediate revocation of signing privileges should credential compromise be suspected; prompt reporting helps limit exposure.

---

*Last reviewed: 2026-04-20.*
