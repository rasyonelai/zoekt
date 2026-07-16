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

// Command zoekt-mirror-bitbucket-cloud discovers and clones Bitbucket Cloud repositories.
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

const (
	bbCloudAPI  = "https://api.bitbucket.org/2.0"
	bbCloudHost = "bitbucket.org"
)

type bbCloudRepo struct {
	FullName string `json:"full_name"`
	Slug     string `json:"slug"`
	Parent   *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
	Project *struct {
		Key string `json:"key"`
	} `json:"project"`
	Links struct {
		Clone []struct {
			Name string `json:"name"`
			Href string `json:"href"`
		} `json:"clone"`
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type bbCloudPage struct {
	Next   string        `json:"next"`
	Values []bbCloudRepo `json:"values"`
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
	workspace := flag.String("workspace", "", "workspace to mirror")
	workspaces := stringList{}
	flag.Var(&workspaces, "workspaces", "workspace (repeatable)")
	projects := stringList{}
	flag.Var(&projects, "projects", "project as workspace/projectKey (repeatable)")
	repos := stringList{}
	flag.Var(&repos, "repos", "repository as workspace/repo_slug (repeatable)")
	token := flag.String("token", "", "file holding API token or app password")
	user := flag.String("user", "x-token-auth", "username for Basic auth (use x-token-auth for token-only)")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	noForks := flag.Bool("no-forks", false, "skip forked repositories")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if *workspace == "" && len(workspaces) == 0 && len(projects) == 0 && len(repos) == 0 {
		log.Fatal("must set --workspace, --workspaces, --projects, or --repos")
	}

	authHeader, err := readAuth(*token, *user)
	if err != nil {
		log.Fatal(err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	destDir := filepath.Join(*dest, bbCloudHost)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Fatal(err)
	}

	var allRepos []bbCloudRepo
	if *workspace != "" {
		found, err := listWorkspaceRepos(client, *workspace, authHeader)
		if err != nil {
			log.Fatal(err)
		}
		allRepos = append(allRepos, found...)
	}
	for _, ws := range workspaces {
		found, err := listWorkspaceRepos(client, ws, authHeader)
		if err != nil {
			log.Fatal(err)
		}
		allRepos = append(allRepos, found...)
	}
	for _, p := range projects {
		found, err := listProjectRepos(client, p, authHeader)
		if err != nil {
			log.Printf("listProjectRepos(%q): %v", p, err)
			continue
		}
		allRepos = append(allRepos, found...)
	}
	for _, r := range repos {
		found, err := getRepo(client, r, authHeader)
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
		if *noForks && r.Parent != nil {
			continue
		}
		slug := r.Slug
		if slug == "" && r.FullName != "" {
			parts := strings.Split(r.FullName, "/")
			if len(parts) == 2 {
				slug = parts[1]
			}
		}
		if slug != "" && filter.Include(slug) {
			trimmed = append(trimmed, r)
		}
	}
	allRepos = trimmed

	if err := cloneRepos(destDir, allRepos); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}

	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, allRepos); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func readAuth(path, user string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("must set --token")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(content))
	if user == "" || user == "x-token-auth" {
		return "Bearer " + token, nil
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)), nil
}

func bbRequest(client *http.Client, requestURL, authHeader string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Bitbucket API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func listWorkspaceRepos(client *http.Client, workspace, authHeader string) ([]bbCloudRepo, error) {
	nextURL := fmt.Sprintf("%s/repositories/%s?pagelen=100", bbCloudAPI, url.PathEscape(workspace))
	var allRepos []bbCloudRepo

	for nextURL != "" {
		body, err := bbRequest(client, nextURL, authHeader)
		if err != nil {
			return nil, err
		}
		var page bbCloudPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		allRepos = append(allRepos, page.Values...)
		nextURL = page.Next
	}
	return allRepos, nil
}

func listProjectRepos(client *http.Client, projectPath, authHeader string) ([]bbCloudRepo, error) {
	parts := strings.Split(projectPath, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("project must be workspace/projectKey, got %q", projectPath)
	}
	workspace, projectKey := parts[0], parts[1]
	nextURL := fmt.Sprintf("%s/repositories/%s?q=%s&pagelen=100", bbCloudAPI, url.PathEscape(workspace), url.QueryEscape(`project.key="`+projectKey+`"`))
	var allRepos []bbCloudRepo

	for nextURL != "" {
		body, err := bbRequest(client, nextURL, authHeader)
		if err != nil {
			return nil, err
		}
		var page bbCloudPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		allRepos = append(allRepos, page.Values...)
		nextURL = page.Next
	}
	return allRepos, nil
}

func getRepo(client *http.Client, repoPath, authHeader string) (bbCloudRepo, error) {
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 {
		return bbCloudRepo{}, fmt.Errorf("repo must be workspace/repo_slug, got %q", repoPath)
	}
	requestURL := fmt.Sprintf("%s/repositories/%s/%s", bbCloudAPI, url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	body, err := bbRequest(client, requestURL, authHeader)
	if err != nil {
		return bbCloudRepo{}, err
	}
	var repo bbCloudRepo
	if err := json.Unmarshal(body, &repo); err != nil {
		return bbCloudRepo{}, err
	}
	return repo, nil
}

func httpsCloneURL(repo bbCloudRepo) string {
	for _, clone := range repo.Links.Clone {
		if clone.Name == "https" {
			return clone.Href
		}
	}
	return ""
}

func repoZoektName(repo bbCloudRepo) string {
	if repo.FullName != "" {
		return filepath.Join(bbCloudHost, repo.FullName)
	}
	return ""
}

func cloneRepos(destDir string, repos []bbCloudRepo) error {
	for _, r := range repos {
		cloneURL := httpsCloneURL(r)
		if cloneURL == "" {
			log.Printf("skip %s: no https clone URL", r.FullName)
			continue
		}

		webURL := r.Links.HTML.Href
		if webURL == "" {
			webURL = "https://" + bbCloudHost + "/" + r.FullName
		}

		config := map[string]string{
			"zoekt.web-url-type": "bitbucket",
			"zoekt.web-url":      webURL,
			"zoekt.name":         repoZoektName(r),
			"zoekt.fork":         marshalBool(r.Parent != nil),
		}

		dest, err := gitindex.CloneRepo(destDir, r.FullName, cloneURL, config)
		if err != nil {
			return err
		}
		if dest != "" {
			fmt.Println(dest)
		}
	}
	return nil
}

func marshalBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []bbCloudRepo) error {
	if len(repos) == 0 {
		return nil
	}
	u, err := url.Parse("https://" + bbCloudHost)
	if err != nil {
		return err
	}

	names := map[string]struct{}{}
	for _, r := range repos {
		names[repoZoektName(r)+".git"] = struct{}{}
	}

	return gitindex.DeleteRepos(destDir, u, names, filter)
}
