package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestGithubModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := requireGithubModule(t, reg)

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"IssueState", resolve.SymEnum},
		{"PullState", resolve.SymEnum},
		{"ReviewEvent", resolve.SymEnum},
		{"MergeMethod", resolve.SymEnum},
		{"Client", resolve.SymStruct},
		{"Repository", resolve.SymStruct},
		{"Pagination", resolve.SymStruct},
		{"IssueQuery", resolve.SymStruct},
		{"IssueDraft", resolve.SymStruct},
		{"IssuePatch", resolve.SymStruct},
		{"PullRequestQuery", resolve.SymStruct},
		{"PullRequestDraft", resolve.SymStruct},
		{"PullRequestPatch", resolve.SymStruct},
		{"ReviewDraft", resolve.SymStruct},
		{"MergeOptions", resolve.SymStruct},
		{"WorkflowRunQuery", resolve.SymStruct},
		{"WorkflowDispatch", resolve.SymStruct},
		{"ReleaseDraft", resolve.SymStruct},
		{"ReleaseNotesOptions", resolve.SymStruct},
		{"WebhookDelivery", resolve.SymStruct},
		{"WebhookDecision", resolve.SymStruct},
		{"ApiError", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.github missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.github.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.github.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"anonymous", "client", "enterprise", "repo", "parseRepo",
		"pagination", "page", "issueQuery", "issueDraft", "issuePatch",
		"pullRequestQuery", "pullRequestDraft", "pullRequestPatch",
		"workflowRunQuery", "workflowDispatch",
		"review", "mergeOptions", "mergeOptionsWithMethod",
		"releaseDraft", "releaseNotesOptions",
		"headers", "apiUrl", "uploadUrl", "repoUrl",
		"issuesUrl", "issueUrl", "issueCommentsUrl",
		"pullsUrl", "pullUrl", "pullFilesUrl", "pullReviewsUrl",
		"actionsRunsUrl", "workflowRunsUrl",
		"releasesUrl", "releaseUrl",
		"listIssuesHttpRequest", "listIssuesWithQueryHttpRequest", "getIssueHttpRequest",
		"createIssueHttpRequest", "updateIssueHttpRequest", "closeIssueHttpRequest", "commentIssueHttpRequest",
		"listPullRequestsHttpRequest", "listPullRequestsWithQueryHttpRequest", "getPullRequestHttpRequest",
		"createPullRequestHttpRequest", "updatePullRequestHttpRequest", "listPullRequestFilesHttpRequest",
		"listPullRequestFilesPageHttpRequest", "createPullRequestReviewHttpRequest",
		"requestPullRequestReviewersHttpRequest", "mergePullRequestHttpRequest", "mergePullRequestWithOptionsHttpRequest",
		"listWorkflowRunsHttpRequest", "listWorkflowRunsWithQueryHttpRequest",
		"listWorkflowRunsForWorkflowHttpRequest", "listWorkflowRunsForWorkflowWithQueryHttpRequest",
		"getWorkflowRunHttpRequest", "listWorkflowJobsHttpRequest", "listWorkflowJobsPageHttpRequest",
		"workflowDispatchHttpRequest", "rerunWorkflowRunHttpRequest", "cancelWorkflowRunHttpRequest",
		"listArtifactsHttpRequest", "listArtifactsPageHttpRequest", "downloadArtifactHttpRequest",
		"listReleasesHttpRequest", "listReleasesPageHttpRequest", "latestReleaseHttpRequest",
		"getReleaseHttpRequest", "getReleaseByTagHttpRequest", "createReleaseHttpRequest",
		"updateReleaseHttpRequest", "deleteReleaseHttpRequest", "generateReleaseNotesHttpRequest",
		"uploadReleaseAssetHttpRequest",
		"execute", "executeJson", "parseApiError", "requireSuccess",
		"webhookSignature", "verifyWebhookSignature", "webhookDelivery", "verifyWebhookRequest",
		"parseWebhookPayload", "webhookAction", "isWebhookEvent", "webhookAck",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.github missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.github.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.github.%s not public", name)
		}
	}
}

func TestGithubModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"IssueState", []string{"toString"}},
		{"PullState", []string{"toString"}},
		{"ReviewEvent", []string{"toString"}},
		{"MergeMethod", []string{"toString"}},
		{"Client", []string{"withToken", "withApiVersion", "withUserAgent", "withHeader"}},
		{"Repository", []string{"fullName", "apiPath"}},
		{"ApiError", []string{"summary"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("github", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(github, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("github.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestGithubModulePinsAutomationBehavior(t *testing.T) {
	src := githubModuleSource(t)
	for _, want := range []string{
		`"https://api.github.com"`,
		`"https://uploads.github.com"`,
		`out.insert("Accept", "application/vnd.github+json")`,
		`out.insert("X-GitHub-Api-Version", strings.trimSpace(client.apiVersion))`,
		`out.insert("Authorization", "Bearer {strings.trimSpace(client.token)}")`,
		`"{repository.apiPath()}/issues/{n}"`,
		`"{repository.apiPath()}/pulls/{n}/merge"`,
		`"{repository.apiPath()}/actions/workflows/{workflow}/dispatches"`,
		`"{repository.apiPath()}/actions/runs/{id}/cancel"`,
		`"{repository.apiPath()}/releases/generate-notes"`,
		`let url = "{uploadUrl(client, path)?}?name={encodedName}"`,
		`prop("maintainer_can_modify", boolValue(draft.maintainerCanModify))`,
		`prop("team_reviewers", stringArray(teamReviewers))`,
		`prop("merge_method", quote(options.method.toString()))`,
		`prop("tag_name", quote(requiredText("release tag", draft.tagName)?))`,
		`prop("inputs", normalizedJsonObject(dispatch.inputsJson))`,
		`let digest = crypto.hmac.sha256(bytes.fromString(secret), body)`,
		`crypto.constantTimeEq(bytes.fromString(webhookSignature(secret, body)), bytes.fromString(clean))`,
		`request.headerOr("X-Hub-Signature-256", "")`,
		`parseApiError(response).summary()`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.github source missing %q", want)
		}
	}
}

func TestGithubImportResolvesAutomationWorkflow(t *testing.T) {
	src := `
use std.github

pub fn demo() -> Result<(), Error> {
    let gh = github.client("token")
    let repo = github.repo("choiceoh", "osty")?
    let mut issues = github.issueQuery()
    issues.state = github.IssueAll
    let _ = github.listIssuesWithQueryHttpRequest(gh, repo, issues)?
    let issue = github.issueDraft("bug")
    let _ = github.createIssueHttpRequest(gh, repo, issue)?
    let pr = github.pullRequestDraft("change", "codex/x", "main")
    let _ = github.createPullRequestHttpRequest(gh, repo, pr)?
    let _ = github.mergePullRequestWithOptionsHttpRequest(gh, repo, 7, github.mergeOptionsWithMethod(github.MergeSquash))?
    let _ = github.workflowDispatchHttpRequest(gh, repo, "ci.yml", github.workflowDispatch("main"))?
    let _ = github.createReleaseHttpRequest(gh, repo, github.releaseDraft("v1.0.0"))?
    Ok(())
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.github fixture: %s: %s", d.Code, d.Message)
	}
}

func requireGithubModule(t *testing.T, reg *Registry) *Module {
	t.Helper()
	mod := reg.Modules["github"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		var details []string
		for _, d := range reg.Diags {
			if d != nil && strings.Contains(d.File+d.Message, "github") {
				details = append(details, d.Error())
			}
		}
		t.Fatalf("std.github not fully loaded; diagnostics=%v", details)
	}
	return mod
}

func githubModuleSource(t *testing.T) string {
	t.Helper()
	return string(requireGithubModule(t, LoadCached()).Source)
}
