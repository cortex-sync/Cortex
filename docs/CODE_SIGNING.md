# Code signing

How Cortex signs its release artifacts, and the path to getting there via the
SignPath Foundation.

## Approach

| Layer | Tool | Status |
|---|---|---|
| **Windows Authenticode** (executable + `.mcpb`) | **SignPath Foundation** (free OSS, Sectigo cert in the Microsoft Trusted Root Program) | Applying - see below |
| **Supply-chain attestation** (provenance, SBOM) | cosign keyless + SLSA (see `docs/TODO.md` M3/M4/M12) | Planned, separate work |
| **macOS notarization** | Apple Developer Program ($99/yr) - no free OSS option | Parked until it's a real ask |

Why SignPath Foundation: it is free for OSS, issues a Sectigo certificate that
**is** in the Microsoft Trusted Root Program (so it builds SmartScreen reputation,
which a sigstore/cosign signature cannot), and has no geographic restriction on
the maintainer. Azure Trusted Signing (~$9.99/mo) was ruled out: it is limited to
US/Canada individuals and US/Canada/EU/UK organisations, and the maintainer is in
Australia. cosign is the right tool for supply-chain provenance but does **not**
satisfy Windows Authenticode, so the two are complementary, not alternatives.

## SignPath Foundation eligibility - Cortex status

SignPath Foundation's [OSS conditions](https://signpath.org/terms.html), mapped to
Cortex:

| Requirement | Status |
|---|---|
| OSI-approved licence, no commercial dual-licensing | ✅ MIT (`LICENSE`) |
| Actively maintained and already released | ✅ v0.1.0 - v0.2.0 published |
| No malware / PUP / hacking tools | ✅ profile-sync tooling only |
| Functionality documented on the download/release page | ✅ `README.md`, `docs/usage.md`, release notes |
| MFA on source control for all team members | ✅ GitHub MFA enabled |
| MFA on SignPath for all team members | ⬜ enable on SignPath signup |
| A "Code signing policy" page on the project homepage | ⬜ add to `README.md` (drafted below) |
| Team roles: author / reviewer / approver | ✅ documented in the policy (solo maintainer) |

## Pre-application checklist

1. ⬜ **Publish the "Code signing policy" section** in `README.md` (draft below).
   SignPath reviewers check for it, using that exact term, during the application.
2. ⬜ **Enable MFA on the SignPath account** at signup (GitHub MFA already done).
3. ⬜ **Submit** the application at <https://signpath.org/apply.html> with the repo
   URL, licence, and a link to the policy section.
4. ⬜ On approval, wire the SignPath signing step into `release.yml` (below) and
   cut a release to exercise it.

## Code signing policy (the page SignPath checks)

The following goes in `README.md` as a `## Code signing policy` section. It must
carry the exact attribution, the team roles, and a privacy statement:

```markdown
## Code signing policy

Free code signing provided by [SignPath.io](https://signpath.io), certificate by
[SignPath Foundation](https://signpath.org).

Cortex's Windows release artifacts (the `cortex-git-server` executable and the
`.mcpb` desktop-extension bundles) are signed with a certificate issued to the
SignPath Foundation, so the publisher shows as "SignPath Foundation". Signing runs
in the release pipeline on tagged releases; every artifact is also listed in
`checksums.txt`, and the launcher verifies each downloaded binary's SHA-256
(fail-closed) before running it.

**Roles**

- **Author / committer:** [Lucas Symons](https://github.com/LucasSymons)
- **Reviewer:** [Lucas Symons](https://github.com/LucasSymons)
- **Approver:** [Lucas Symons](https://github.com/LucasSymons)

Cortex is maintained by a single author; the roles are separated here per the
signing policy and will be assigned to distinct maintainers as the team grows.

**Privacy policy**

Cortex collects, transmits, and stores no personal data. It runs locally and
communicates only with the Git host you configure, using credentials you supply;
nothing is sent elsewhere. The signing pipeline processes only the released build
artifacts.
```

> Timing: this asserts the SignPath arrangement, so publish it as part of
> submitting the application (SignPath expect the page to exist during review).
> The v0.2.0 binaries already shipped **unsigned**; the first *signed* release is
> the one cut after approval.

## Post-approval: wiring signing into the release

SignPath signs via a GitHub Action that submits the built artifact to their API
and returns the signed file. It slots into `release.yml` **after** goreleaser
builds the binaries and **before** they are attached to the release / packed into
`.mcpb` bundles, so the signed executable is what ships. It needs the SignPath
organisation ID, project/signing-policy slugs, and an API token (as a repo
secret). Sign the Windows `cortex-git-server.exe` (and the `.mcpb` payloads);
Linux/macOS binaries are not Authenticode-signed. Detailed once the project slugs
exist post-approval.

## Sources

- [SignPath Foundation - OSS conditions](https://signpath.org/terms.html)
- [SignPath Foundation - apply](https://signpath.org/apply.html)
- [SignPath for Open Source](https://signpath.io/solutions/open-source-community)
