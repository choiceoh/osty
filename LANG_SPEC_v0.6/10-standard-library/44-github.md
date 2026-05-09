### 10.44 GitHub (`std.github`)

`std.github` is a GitHub automation layer over `std.http`, `std.json`, and
`std.crypto`. It builds ready-to-send REST requests for day-to-day developer
automation and verifies GitHub webhook signatures without taking ownership of
token storage, HTTP hosting, or background job scheduling.

```osty
use std.env
use std.github

// `std.github` builds `Request` values; sending them is a `Net` effect.
// Token lookup is an `Env` effect. Both are explicit capability params.
fn buildIssueListRequest(env: Env, owner: String, name: String) -> Result<HttpRequest, Error> {
    let gh = github.client(env.require("GITHUB_TOKEN")?)
    let repo = github.repo(owner, name)?

    let mut issues = github.issueQuery()
    issues.state = github.IssueAll
    github.listIssuesWithQueryHttpRequest(gh, repo, issues)
}

fn submitDraftPr(env: Env, net: Net) -> Result<PullRequest, Error> {
    let gh = github.client(env.require("GITHUB_TOKEN")?)
    let repo = github.repo("choiceoh", "osty")?

    let pr = github.pullRequestDraft("fix std.github docs", "codex/std-github", "main")
    let req = github.createPullRequestHttpRequest(gh, repo, pr)?
    let resp = net.httpClient().request(req)?.requireStatus(201)?
    resp.json::<PullRequest>()
}
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

#### 10.44.1 GitHub capability surface

Production GitHub code routes through:

1. **`Env`** for token lookup — `env.require("GITHUB_TOKEN")`.
2. **`Net`** for HTTP transport — `client.send(net, req)`.
3. **`std.crypto`** for webhook signature verification — pure,
   no capability needed.

Pure builder functions (`github.repo`, `github.issueQuery`,
`github.pullRequestDraft`, etc.) build request values and are
acceptable inside `#[pure]`. Only the
actual `send*` call is non-reproducible.

#### 10.44.2 Webhook verification flow

GitHub webhooks use HMAC-SHA256 signature verification. The
verification function:

```osty
fn verifyWebhook(secret: String, body: Bytes, sigHeader: String) -> Bool {
    let expected = crypto.hmac.sha256(secret.toBytes(), body)
    let actual = parseSig(sigHeader)
    crypto.constantTimeEq(expected, actual)
}
```

is *pure* — no capability required. The `constantTimeEq` is
critical: a vanilla `==` on byte sequences leaks timing
information that attackers can exploit to forge signatures.

The full webhook intake combines verification + replay window +
payload parsing — covered by `std.webhook` (§10.45).

#### 10.44.3 Rate limit handling

GitHub returns rate-limit headers (`X-RateLimit-Remaining`,
`X-RateLimit-Reset`) on every response. Client code that wants to
respect these should:

1. Read the headers from the `Response`.
2. Sleep until reset if remaining is 0.
3. Retry the request.

`std.httpretry` (§10.34) provides the backoff infrastructure;
`Clock.sleep` honors cancellation so a long-running rate-limit
wait does not block `taskGroup` shutdown.

#### 10.44.4 Token scope and `#[stability]`

A `pub fn` that takes `github.Client` exposes the underlying token
*usage* — code that wraps GitHub calls should document the minimum
required token scope (e.g. `repo`, `repo:read`, `workflow`).

Documenting scope is a `#[purpose]` / `#[doc-comment]` choice, not
a v0.6 type-level feature. A future audit subcommand
(`osty audit --github-scope`) is tracked for Phase 5 to
auto-extract scope hints from doc comments.
