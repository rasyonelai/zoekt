// Copyright 2026 Rasyonel AI. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command zoekt-mirror-ado discovers and clones Azure DevOps repositories.
// Supports Azure DevOps Cloud and Server (with optional /tfs path).
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt/gitindex"
)

const adoAPIVersion = "7.1"

type adoProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type adoRepo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RemoteURL  string `json:"remoteUrl"`
	IsDisabled bool   `json:"isDisabled"`
	Size       int64  `json:"size"`
	Project    struct {
		Name string `json:"name"`
	} `json:"project"`
}

type adoListResponse[T any] struct {
	Value []T `json:"value"`
}

type stringList []string

func (f *stringList) String() string {
	return strings.Join(*f, ",")
}

func (f *stringList) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	dest := flag.String("dest", "", "destination directory")
	baseURL := flag.String("url", "https://dev.azure.com", "Azure DevOps base URL")
	org := flag.String("org", "", "organization or collection to mirror")
	orgs := stringList{}
	flag.Var(&orgs, "orgs", "organization or collection (repeatable)")
	projects := stringList{}
	flag.Var(&projects, "projects", "project as org/project (repeatable)")
	repos := stringList{}
	flag.Var(&repos, "repos", "repository as org/project/repo (repeatable)")
	token := flag.String("token", "", "file holding Personal Access Token")
	useTfsPath := flag.Bool("use-tfs-path", false, "include /tfs in org URL (Azure DevOps Server)")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if *org == "" && len(orgs) == 0 && len(projects) == 0 && len(repos) == 0 {
		log.Fatal("must set --org, --orgs, --projects, or --repos")
	}

	pat, err := readToken(*token)
	if err != nil {
		log.Fatal(err)
	}

	rootURL, err := url.Parse(strings.TrimRight(*baseURL, "/"))
	if err != nil {
		log.Fatal(err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	destDir := filepath.Join(*dest, rootURL.Host)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	var allRepos []adoRepo
	if *org != "" {
		found, err := listOrgRepos(client, rootURL, *org, pat, *useTfsPath)
		if err != nil {
			log.Fatal(err)
		}
		allRepos = append(allRepos, found...)
	}
	for _, o := range orgs {
		found, err := listOrgRepos(client, rootURL, o, pat, *useTfsPath)
		if err != nil {
			log.Fatal(err)
		}
		allRepos = append(allRepos, found...)
	}
	for _, p := range projects {
		found, err := listProjectRepos(client, rootURL, p, pat, *useTfsPath)
		if err != nil {
			log.Fatal(err)
		}
		allRepos = append(allRepos, found...)
	}
	for _, r := range repos {
		found, err := getRepo(client, rootURL, r, pat, *useTfsPath)
		if err != nil {
			log.Printf("getRepo(%q): %v", r, err)
			continue
		}
		allRepos = append(allRepos, found)
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	trimmed := allRepos[:0]
	for _, r := range allRepos {
		if r.IsDisabled || r.RemoteURL == "" {
			continue
		}
		if filter.Include(r.Name) {
			trimmed = append(trimmed, r)
		}
	}
	allRepos = trimmed

	if err := cloneRepos(destDir, rootURL.Host, allRepos); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}

	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, allRepos, rootURL.Host); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("must set --token")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

func buildOrgURL(base *url.URL, org string, useTfsPath bool) string {
	tfsSegment := ""
	if useTfsPath {
		tfsSegment = "/tfs"
	}
	return fmt.Sprintf("%s%s/%s", strings.TrimRight(base.String(), "/"), tfsSegment, org)
}

func adoRequest[T any](client *http.Client, requestURL, pat string) (T, error) {
	var zero T
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+pat)))
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("ADO API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return zero, err
	}
	return result, nil
}

func listOrgRepos(client *http.Client, base *url.URL, org, pat string, useTfsPath bool) ([]adoRepo, error) {
	orgURL := buildOrgURL(base, org, useTfsPath)
	projectsURL := fmt.Sprintf("%s/_apis/projects?api-version=%s&$top=1000", orgURL, adoAPIVersion)
	projectsResp, err := adoRequest[adoListResponse[adoProject]](client, projectsURL, pat)
	if err != nil {
		return nil, err
	}

	var allRepos []adoRepo
	for _, project := range projectsResp.Value {
		reposURL := fmt.Sprintf("%s/%s/_apis/git/repositories?api-version=%s", orgURL, url.PathEscape(project.Name), adoAPIVersion)
		reposResp, err := adoRequest[adoListResponse[adoRepo]](client, reposURL, pat)
		if err != nil {
			log.Printf("list repos for project %s: %v", project.Name, err)
			continue
		}
		allRepos = append(allRepos, reposResp.Value...)
	}
	return allRepos, nil
}

func listProjectRepos(client *http.Client, base *url.URL, projectPath, pat string, useTfsPath bool) ([]adoRepo, error) {
	parts := strings.Split(projectPath, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("project must be org/project, got %q", projectPath)
	}
	orgURL := buildOrgURL(base, parts[0], useTfsPath)
	reposURL := fmt.Sprintf("%s/%s/_apis/git/repositories?api-version=%s", orgURL, url.PathEscape(parts[1]), adoAPIVersion)
	reposResp, err := adoRequest[adoListResponse[adoRepo]](client, reposURL, pat)
	if err != nil {
		return nil, err
	}
	return reposResp.Value, nil
}

func getRepo(client *http.Client, base *url.URL, repoPath, pat string, useTfsPath bool) (adoRepo, error) {
	parts := strings.Split(repoPath, "/")
	if len(parts) != 3 {
		return adoRepo{}, fmt.Errorf("repo must be org/project/repo, got %q", repoPath)
	}
	orgURL := buildOrgURL(base, parts[0], useTfsPath)
	repoURL := fmt.Sprintf("%s/%s/_apis/git/repositories/%s?api-version=%s", orgURL, url.PathEscape(parts[1]), url.PathEscape(parts[2]), adoAPIVersion)
	return adoRequest[adoRepo](client, repoURL, pat)
}

func repoZoektName(host string, repo adoRepo) string {
	project := repo.Project.Name
	if project == "" {
		project = "unknown"
	}
	return filepath.Join(host, project, repo.Name)
}

func cloneRepos(destDir, host string, repos []adoRepo) error {
	for _, r := range repos {
		config := map[string]string{
			"zoekt.web-url-type": "azuredevops",
			"zoekt.web-url":      strings.TrimSuffix(r.RemoteURL, ".git"),
			"zoekt.name":         repoZoektName(host, r),
		}

		dest, err := gitindex.CloneRepo(destDir, filepath.Join(r.Project.Name, r.Name), r.RemoteURL, config)
		if err != nil {
			return err
		}
		if dest != "" {
			fmt.Println(dest)
		}
	}
	return nil
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []adoRepo, host string) error {
	if len(repos) == 0 {
		return nil
	}
	u, err := url.Parse("https://" + host)
	if err != nil {
		return err
	}

	names := map[string]struct{}{}
	for _, r := range repos {
		names[repoZoektName(host, r)+".git"] = struct{}{}
	}

	return gitindex.DeleteRepos(destDir, u, names, filter)
}
