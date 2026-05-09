### 10.44 GitHub (`std.github`)

- **Scope**: Osty stdlib spec — 10.44 GitHub (`std.github`)
- **Type**: Standard library specification
`std.github` is a GitHub automation layer over `std.http`, `std.json`, and
`std.crypto`. It builds ready-to-send REST requests for day-to-day developer
automation and verifies GitHub webhook signatures without taking ownership of
token storage, HTTP hosting, or background job scheduling.

```osty
use std.env
use std.github

let gh = github.client(env.require("GITHUB_TOKEN")?)
let repo = github.repo("choiceoh", "osty")?

let mut issues = github.issueQuery()
issues.state = github.IssueAll
let req = github.listIssuesWithQueryHttpRequest(gh, repo, issues)?

let pr = github.pullRequestDraft("fix std.github docs", "codex/std-github", "main")
let create = github.createPullRequestHttpRequest(gh, repo, pr)?
```

Core types:

```osty
pub struct Client
pub struct Repository
pub struct Pagination
pub struct IssueQuery
pub struct IssueDraft
pub struct IssuePatch
pub struct PullRequestQuery
pub struct PullRequestDraft
pub struct PullRequestPatch
pub struct ReviewDraft
pub struct MergeOptions
pub struct WorkflowRunQuery
pub struct WorkflowDispatch
pub struct ReleaseDraft
pub struct ReleaseNotesOptions
pub struct WebhookDelivery
pub struct WebhookDecision
pub struct ApiError

pub enum IssueState { IssueOpen, IssueClosed, IssueAll }
pub enum PullState { PullOpen, PullClosed, PullAll }
pub enum ReviewEvent { ReviewComment, ReviewApprove, ReviewRequestChanges }
pub enum MergeMethod { MergeCommit, MergeSquash, MergeRebase }
```

Configuration:

```osty
github.anonymous() -> Client
github.client(token) -> Client
github.enterprise(baseUrl, uploadBaseUrl, token) -> Result<Client, Error>
github.repo(owner, name) -> Result<Repository, Error>
github.parseRepo("owner/name") -> Result<Repository, Error>

client.withToken(token)
client.withApiVersion(version)
client.withUserAgent(value)
client.withHeader(name, value)
```

`headers(client)` emits `Accept: application/vnd.github+json`,
`X-GitHub-Api-Version`, `User-Agent`, and `Authorization: Bearer ...` when a
token is present. Extra headers are applied last so hosts can tune previews or
enterprise gateway behavior.

Issues:

```osty
github.issueQuery()
github.issueDraft(title)
github.issuePatch()

github.listIssuesHttpRequest(client, repo)
github.listIssuesWithQueryHttpRequest(client, repo, query)
github.getIssueHttpRequest(client, repo, number)
github.createIssueHttpRequest(client, repo, draft)
github.updateIssueHttpRequest(client, repo, number, patch)
github.closeIssueHttpRequest(client, repo, number)
github.commentIssueHttpRequest(client, repo, number, body)
```

Pull requests:

```osty
github.pullRequestQuery()
github.pullRequestDraft(title, head, base)
github.pullRequestPatch()
github.review(event, body)
github.mergeOptions()
github.mergeOptionsWithMethod(method)

github.listPullRequestsHttpRequest(client, repo)
github.listPullRequestsWithQueryHttpRequest(client, repo, query)
github.getPullRequestHttpRequest(client, repo, number)
github.createPullRequestHttpRequest(client, repo, draft)
github.updatePullRequestHttpRequest(client, repo, number, patch)
github.listPullRequestFilesHttpRequest(client, repo, number)
github.listPullRequestFilesPageHttpRequest(client, repo, number, pagination)
github.createPullRequestReviewHttpRequest(client, repo, number, review)
github.requestPullRequestReviewersHttpRequest(client, repo, number, reviewers, teamReviewers)
github.mergePullRequestHttpRequest(client, repo, number)
github.mergePullRequestWithOptionsHttpRequest(client, repo, number, options)
```

Actions:

```osty
github.workflowRunQuery()
github.workflowDispatch(ref)

github.listWorkflowRunsHttpRequest(client, repo)
github.listWorkflowRunsWithQueryHttpRequest(client, repo, query)
github.listWorkflowRunsForWorkflowHttpRequest(client, repo, workflowId)
github.listWorkflowRunsForWorkflowWithQueryHttpRequest(client, repo, workflowId, query)
github.getWorkflowRunHttpRequest(client, repo, runId)
github.listWorkflowJobsHttpRequest(client, repo, runId)
github.listWorkflowJobsPageHttpRequest(client, repo, runId, pagination)
github.workflowDispatchHttpRequest(client, repo, workflowId, dispatch)
github.rerunWorkflowRunHttpRequest(client, repo, runId)
github.cancelWorkflowRunHttpRequest(client, repo, runId)
github.listArtifactsHttpRequest(client, repo)
github.listArtifactsPageHttpRequest(client, repo, pagination)
github.downloadArtifactHttpRequest(client, repo, artifactId, archiveFormat)
```

Releases:

```osty
github.releaseDraft(tagName)
github.releaseNotesOptions(tagName)

github.listReleasesHttpRequest(client, repo)
github.listReleasesPageHttpRequest(client, repo, pagination)
github.latestReleaseHttpRequest(client, repo)
github.getReleaseHttpRequest(client, repo, releaseId)
github.getReleaseByTagHttpRequest(client, repo, tagName)
github.createReleaseHttpRequest(client, repo, draft)
github.updateReleaseHttpRequest(client, repo, releaseId, draft)
github.deleteReleaseHttpRequest(client, repo, releaseId)
github.generateReleaseNotesHttpRequest(client, repo, options)
github.uploadReleaseAssetHttpRequest(client, repo, releaseId, name, body, contentType)
```

Webhooks:

```osty
github.webhookSignature(secret, body) -> String
github.verifyWebhookSignature(secret, body, signatureHeader) -> Bool
github.webhookDelivery(request) -> WebhookDelivery
github.verifyWebhookRequest(request, secret) -> WebhookDecision
github.parseWebhookPayload(request) -> Result<json.Json, Error>
github.webhookAction(body) -> String?
github.isWebhookEvent(request, eventName) -> Bool
github.webhookAck(message) -> http.Response
```

`webhookSignature` renders the `sha256=` value GitHub sends in
`X-Hub-Signature-256`. Verification uses HMAC-SHA256 and constant-time byte
comparison through `std.crypto`.

Execution helpers:

```osty
github.execute(request) -> Result<http.Response, Error>
github.executeJson<T>(request) -> Result<T, Error>
github.parseApiError(response) -> ApiError
github.requireSuccess(response) -> Result<http.Response, Error>
```

`ApiError` extracts GitHub's common `message` and `documentation_url` fields
while preserving the raw body for logs and agent transcripts.
