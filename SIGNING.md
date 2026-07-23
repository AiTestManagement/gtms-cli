# Code Signing Policy

## Current status

**GTMS release binaries are not currently Authenticode-signed.** On Windows, SmartScreen shows *"Windows protected your PC"* with **Unknown publisher** -- this is expected. The binary is safe to run (click **More info -> Run anyway**), and `go install github.com/aitestmanagement/gtms-cli/cmd/gtms@latest` avoids the prompt entirely by building locally. This is the standard distribution pattern for Go CLIs -- ripgrep, fd, bat, and most others ship the same way.

The rest of this document describes the signing policy that **will take effect if and when** GTMS adopts code signing (the current plan is a SignPath Foundation application). Until that lands, read it as the intended future state, not a description of today's releases.

## Planned signing authority

If adopted, GTMS would use [SignPath Foundation](https://signpath.org/) for Authenticode code signing under their free programme for open-source projects. Signing would be performed through SignPath's managed service; no signing keys would be held by the GTMS project or by any individual maintainer.

## Artefacts that would be covered

- Windows binaries (`gtms.exe` for `windows/amd64` and `windows/arm64`) attached to GitHub Releases.
- Windows installers (MSI / winget packages) when those distribution channels go live.
- macOS and Linux binaries are not Authenticode-signed -- Authenticode applies to Windows only.

Once signing is live, it would apply to the first release after approval and to all subsequent releases; earlier releases remain unsigned.

## Planned roles and approvals

GTMS is currently a solo-maintained project. Under the planned policy, all SignPath signing roles are held by the project maintainer team:

- **Author** -- the GTMS maintainer -- produces the release artefact.
- **Reviewer** -- the GTMS maintainer -- manually reviews the built artefact before requesting signing.
- **Approver** -- the GTMS maintainer -- approves the signing request in SignPath's portal before the signature is applied.

Every signing request would be manually approved; automated or unattended signing would not be used. If additional maintainers join the project, this document will be updated to distribute the roles across maintainers rather than collapsing them.

## Build provenance

- Release binaries are built in this public repository on GitHub-hosted runners via `.github/workflows/release.yml`, triggered by a `v*` tag push.
- Once signing is live, every signed binary would be traceable from the release tag to the corresponding workflow run and source commit, and SignPath's GitHub Action would submit the built artefact for signing after the build step and before upload to the GitHub Release.

## Verifying a signed binary (once signing is live)

When a release is signed, you will be able to verify it on Windows using `signtool` from the Windows SDK:

```
signtool verify /pa /v gtms.exe
```

Expected, for a signed release: exit code 0, a valid Authenticode signature chain with SignPath Foundation as the signer, and a valid timestamp. **Current releases are unsigned and will not pass this check -- that is expected, not a tampering signal.**

## Reporting a signing concern

If, once signing is live, you observe a GTMS binary that appears to have been signed outside this policy, or you believe the signing credentials have been misused, please report it through either of the following:

- **Email** -- <contact@testmanagement.com>
- **GitHub Security Advisories** -- https://github.com/aitestmanagement/gtms-cli/security/advisories (private disclosure via GitHub)

SignPath Foundation's portal allows immediate revocation of signing privileges should credential compromise be suspected; prompt reporting helps limit exposure.

---

*Last reviewed: 2026-07-23 (pre-v0.3.1 tag).*
