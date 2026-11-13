# Unified template integration

Status: candidate client for an agreed MGR user API, not deployed acceptance.
No unreleased contracts dependency is pinned: these user endpoints are MGR-owned.
The server implementation and its published exact protocol must be verified before release.

## Candidate commands

Commands below are templates: substitute the actual space, Web, template, request,
revision and file identities. Run them only against a matching verified service.
The default `publish-share-template WEB_ID` continues to use the legacy API.
No candidate error falls back to a legacy write.

```sh
# First publication from an existing Web. Save the request ID before running.
tiana web publish-share-template WEB_ID --unified --request-id REQUEST_ID --file INSTALL_ZIP --wait --json
# Update the SAME template and original Web; use a new request for a new publication.
tiana web publish-share-template WEB_ID --template-id TEMPLATE_ID --request-id REQUEST_ID --file INSTALL_ZIP --wait --json
tiana web status-template TEMPLATE_ID --json
tiana web status-template TEMPLATE_ID --job-id JOB_ID --json
tiana web list-owned-templates --limit 20 --json
# Preserve this entire input for recovery; read revision from status first.
tiana web set-template-distribution TEMPLATE_ID --request-id REQUEST_ID --expected-revision REVISION --share-enabled=false --json
tiana web init-unified-template TEMPLATE_ID --channel market --version VERSION --dir NEW_DIRECTORY --json
```

Creation uses `{source_web_id,request_id}`; replay returns the original identity.
The first publication's detail has null current_version until publication succeeds.
An update checks the template still belongs to the original Web; a deleted source
rejects publication, but does not itself disable distribution. The stable template
ID, source Web ID, job ID, revision, published version and material hash are separate
identities. Creating a template is not permission to change its source or reuse a
retired template/link after deletion.

The source command initializes an editable project, not an installed Web. New
`/templates/{template_id}` share links use the share-channel source descriptor;
old `/shared-templates/{web_id}` links retain their old source request. The server
owns permanent alias/tombstone mapping; the client never rewrites an old link to
another template. A source-unavailable response causes no object download, no
installation-archive fallback and no target-directory creation.

## Aligned adapter protocol

| Operation | Candidate MGR request | Client checks |
| --- | --- | --- |
| Create | `POST /api/v1/templates` `{source_web_id,request_id}` | Stable ID and original Web in returned detail |
| Owner page | `GET /api/v1/templates?limit=1..100&after=OPAQUE` | One page; items retain detail and server permissions |
| Owner detail | `GET /api/v1/templates/{id}` | Original template identity and independent distribution fields |
| Begin version | `POST /api/v1/templates/{id}/uploads` `{request_id,size}` | Template/job binding, status, exact direct-upload form; size is a JSON number |
| Complete | `POST .../uploads/{job_id}/complete` `{}` | Same template/job identity; authorization is never printed |
| Publication readback | `GET .../publications/{job_id}` | Immutable job published_version, not a newer current_version |
| Distribution | `PUT .../distribution` `{request_id,expected_revision,share_enabled?,market_enabled?}` | Fixed request and before/after revision/state; only supplied fields change |
| Source | `GET /api/v1/template-catalog/{id}/source?channel=share\|market&version=VERSION` | Template, original Web, selected version, size and SHA-256 |

Versions and revisions remain decimal strings. Current version retains
material_version, has_source and installable. Management permissions remain
server-derived: can_publish_version, can_change_share, can_change_market,
can_delete, can_download_source. Counters preserve share_install_count,
market_install_count and market_copy_count as three separate decimal strings;
they use different event definitions and are never added together.

Publication statuses are uploading, pending, running, fail and complete. Only
complete with a non-null valid published_version is success. Fail is a definite
job failure; unknown status, incomplete receipt, network error or 503 is not success
and does not authorize another submission. Query the original job. If begin's
response is lost before its job ID is known, replay only the original begin request
with the exact original archive; never generate a new request as recovery.

Distribution success is the fixed receipt, not a later GET state. Preserve original
request ID, expected revision and supplied switches on ambiguous results; an explicit
replay uses that exact body. The client makes no automatic mutation replay. A CAS
conflict or same-request/different-input conflict is returned without fallback.
Sharing and market listing are independent and use the same current version.
Publication does not enable either channel or upgrade previously installed apps.

No-source pure frontend versions remain install-only; source ZIP is never inferred
from site.tweb. Function build dependencies and current file-package requirements
remain enforced by the existing packer. Source-present to absent and reverse updates
publish a new version under the same template ID; actual server behavior awaits integration.

## Integration still required

This branch does not deploy or publish packages. Verify the MGR implementation's
exact shapes, status/error encodings, role checks, storage behavior and aliases.
Exercise creation replay, same-ID version updates, source-deleted rejection,
independent channels, no-source transitions, immutable job readback after a newer
version, unknown-write recovery, legacy CLI writes and old-link tombstones.

Known candidate errors include 409 TEMPLATE_SOURCE_UNAVAILABLE,
409 TEMPLATE_SOURCE_DELETED, 409 TEMPLATE_REVISION_CONFLICT, 409 REQUEST_ID_CONFLICT,
and 503 TEMPLATE_INSTALLATION_MAINTENANCE. Other 503 and commit uncertainty remain
unknown outcomes; never infer rollback from a status code alone.

Template deletion, public installation and automatic catalog-generation selection
are not implemented by these commands. The server's new/legacy entry configuration
must not implicitly reroute an already submitted operation. New selection config
requires monotonic revision handling if adopted by a future client flow. Local HTTP
fixtures establish client behavior only, not live MGR/OSS compatibility.
