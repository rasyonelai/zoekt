package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcegraph/zoekt/gitindex"
)

func TestBuildOrgURL(t *testing.T) {
	base, _ := url.Parse("https://dev.azure.com")

	tests := []struct {
		name       string
		base       *url.URL
		org        string
		useTfsPath bool
		want       string
	}{
		{
			name: "cloud",
			base: base,
			org:  "acme",
			want: "https://dev.azure.com/acme",
		},
		{
			name:       "server classic tfs",
			base:       mustParseURL(t, "https://ado.example.com"),
			org:        "DefaultCollection",
			useTfsPath: true,
			want:       "https://ado.example.com/tfs/DefaultCollection",
		},
		{
			name: "server root collection",
			base: mustParseURL(t, "https://ado.example.com"),
			org:  "DefaultCollection",
			want: "https://ado.example.com/DefaultCollection",
		},
		{
			name: "virtual directory in base",
			base: mustParseURL(t, "https://azuredevops.example.com/collection"),
			org:  "LCW",
			want: "https://azuredevops.example.com/collection/LCW",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOrgURL(tt.base, tt.org, tt.useTfsPath)
			if got != tt.want {
				t.Fatalf("buildOrgURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateUseTfsPath(t *testing.T) {
	err := validateUseTfsPath(mustParseURL(t, "https://ado.example.com/tfs"), true)
	if err == nil {
		t.Fatal("expected error when base URL already contains /tfs and use-tfs-path is true")
	}

	if err := validateUseTfsPath(mustParseURL(t, "https://ado.example.com/tfs"), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepoZoektName(t *testing.T) {
	repo := adoRepo{Name: "api"}
	repo.Project.Name = "Platform"

	got := repoZoektName("dev.azure.com", "acme", repo)
	want := "dev.azure.com/acme/Platform/api"
	if got != want {
		t.Fatalf("repoZoektName() = %q, want %q", got, want)
	}
}

func TestRepoClonePath(t *testing.T) {
	repo := adoRepo{Name: "api"}
	repo.Project.Name = "Platform"

	got := repoClonePath("DefaultCollection", repo)
	want := "DefaultCollection/Platform/api"
	if got != want {
		t.Fatalf("repoClonePath() = %q, want %q", got, want)
	}
}

func TestAdoListAllPagination(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{
				"count": 1,
				"value": [{"id":"1","name":"Platform"}],
				"continuationToken": "next-page"
			}`))
			return
		}
		if r.URL.Query().Get("continuationToken") != "next-page" {
			t.Fatalf("expected continuationToken=next-page, got %q", r.URL.Query().Get("continuationToken"))
		}
		_, _ = w.Write([]byte(`{
			"count": 1,
			"value": [{"id":"2","name":"Payments"}]
		}`))
	}))
	defer server.Close()

	client := server.Client()
	projects, err := adoListAll[adoProject](client, server.URL+"?api-version=7.1", "pat")
	if err != nil {
		t.Fatalf("adoListAll: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if calls != 2 {
		t.Fatalf("expected 2 HTTP calls, got %d", calls)
	}
}

func TestListOrgReposPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/_apis/projects"):
			_, _ = w.Write([]byte(`{
				"count": 1,
				"value": [{"id":"1","name":"Platform"}]
			}`))
		case strings.Contains(r.URL.Path, "/Platform/_apis/git/repositories") &&
			r.URL.Query().Get("continuationToken") == "repo-page-2":
			_, _ = w.Write([]byte(`{
				"count": 1,
				"value": [
					{"name":"jobs","remoteUrl":"https://dev.azure.com/acme/Platform/_git/jobs","project":{"name":"Platform"}}
				]
			}`))
		case strings.Contains(r.URL.Path, "/Platform/_apis/git/repositories"):
			_, _ = w.Write([]byte(`{
				"count": 2,
				"value": [
					{"name":"api","remoteUrl":"https://dev.azure.com/acme/Platform/_git/api","project":{"name":"Platform"}},
					{"name":"web","remoteUrl":"https://dev.azure.com/acme/Platform/_git/web","project":{"name":"Platform"}}
				],
				"continuationToken": "repo-page-2"
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
	}))
	defer server.Close()

	client := server.Client()
	base := mustParseURL(t, server.URL)
	repos, err := listOrgRepos(client, base, "acme", "pat", false)
	if err != nil {
		t.Fatalf("listOrgRepos: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(repos))
	}
	if repos[0].scope != "acme" {
		t.Fatalf("expected scope acme, got %q", repos[0].scope)
	}
	if got := repoZoektName(base.Host, repos[0].scope, repos[0].repo); got != base.Host+"/acme/Platform/api" {
		t.Fatalf("unexpected zoekt name: %q", got)
	}
}

func TestDeleteStaleReposUsesScopedZoektName(t *testing.T) {
	filter, err := gitindex.NewFilter("", "")
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(destDir, "dev.azure.com"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	repo := adoRepo{Name: "api"}
	repo.Project.Name = "Platform"
	repos := []adoScopedRepo{{scope: "acme", repo: repo}}

	if err := deleteStaleRepos(destDir, filter, repos, "dev.azure.com"); err != nil {
		t.Fatalf("deleteStaleRepos: %v", err)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}
