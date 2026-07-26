package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sourcegraph/zoekt/gitindex"
)

func TestRepoZoektNameBitbucketCloud(t *testing.T) {
	repo := bbCloudRepo{FullName: "acme/api"}
	got := repoZoektName(repo)
	want := "bitbucket.org/acme/api"
	if got != want {
		t.Fatalf("repoZoektName() = %q, want %q", got, want)
	}
}

func TestListWorkspaceReposPagination(t *testing.T) {
	calls := 0
	var nextPageURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{
				"values": [{"full_name":"acme/api","slug":"api","links":{"clone":[{"name":"https","href":"https://bitbucket.org/acme/api.git"}],"html":{"href":"https://bitbucket.org/acme/api"}}}],
				"next": "` + nextPageURL + `"
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"values": [{"full_name":"acme/web","slug":"web","links":{"clone":[{"name":"https","href":"https://bitbucket.org/acme/web.git"}],"html":{"href":"https://bitbucket.org/acme/web"}}}]
		}`))
	}))
	defer server.Close()
	nextPageURL = server.URL + "/page2"

	origAPI := bbCloudAPI
	bbCloudAPI = server.URL
	defer func() { bbCloudAPI = origAPI }()

	client := server.Client()
	repos, err := listWorkspaceRepos(client, "acme", "Bearer token")
	if err != nil {
		t.Fatalf("listWorkspaceRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if calls != 2 {
		t.Fatalf("expected 2 HTTP calls, got %d", calls)
	}
}

func TestWorkspaceRepoForkFilter(t *testing.T) {
	parent := struct {
		FullName string `json:"full_name"`
	}{FullName: "acme/upstream"}
	repo := bbCloudRepo{
		FullName: "acme/fork",
		Slug:     "fork",
		Parent:   &parent,
	}

	filter, err := gitindex.NewFilter(".*", "")
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	if !filter.Include(repo.Slug) {
		t.Fatal("expected slug to match filter")
	}
}
