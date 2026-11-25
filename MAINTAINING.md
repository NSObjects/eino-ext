# Maintenance policy

This repository is an independently maintained community fork of [cloudwego/eino-ext](https://github.com/cloudwego/eino-ext). It is not an official CloudWeGo release channel. Original copyright, license and contributor history are retained.

## Scope

- Prioritize correctness, security, resource lifetime, concurrency and API compatibility.
- Keep the existing module paths during compatibility maintenance. Avoid unrelated refactors and breaking changes.
- Track upstream changes and releases; do not automatically merge upstream pull requests.

## Change process

1. Open an issue describing the problem, affected modules, upstream references and acceptance criteria.
2. Check whether the fix already exists, whether other PRs overlap, and whether dependencies are needed.
3. Reproduce defects with regression tests before applying the fix where practical.
4. Port only relevant commits, retaining authorship and linking the upstream PR and commit SHA. Document any adaptations.
5. Review the diff and run CI before merging; do not bypass failing checks. Use a separate PR for each independently reviewable change.
6. Document compatibility and migration impact in release notes.

## Validation

- Test modified modules and reverse dependencies with a workspace, and verify each module can resolve and test its published dependencies independently (GOWORK=off).
- Coordinate core Eino compatibility and shared ACL dependencies.
- Keep offline unit tests separate from credentialed provider integration tests.
- Never expose secrets to untrusted PR code; use least-privilege workflow permissions.

## Releases

Release each affected module with its own path-prefixed SemVer tag, for example `components/model/openai/v0.1.14-nsobjects.1`. Example versions are illustrative, not published versions. Use a distinct prerelease identifier to avoid collisions with upstream tags. Verify the actual consumer module graph with explicit replacements before release. Never overwrite a published tag. Record upstream baseline, fixes, contributors and compatibility in release notes.

## Upstream triage

Classify candidates as accepted, needs investigation, duplicate, superseded, deferred or rejected. Record the upstream URL and the reason for each decision. Prioritize bug fixes over feature additions; do not treat PR titles as evidence of correctness.
