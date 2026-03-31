package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ContinuumApp/continuum-plugins/catalog"
)

const githubAPIVersion = "2022-11-28"

func main() {
	var repo string
	var tag string
	var manifestPath string

	flag.StringVar(&repo, "repo", "", "GitHub repository in owner/name form")
	flag.StringVar(&tag, "tag", "", "Git tag to publish from")
	flag.StringVar(&manifestPath, "manifest", "manifest.json", "Path to the catalog manifest")
	flag.Parse()

	if strings.TrimSpace(repo) == "" {
		exitf("repo is required")
	}
	if strings.TrimSpace(tag) == "" {
		exitf("tag is required")
	}

	token := os.Getenv("GITHUB_TOKEN")
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	release, err := fetchRelease(ctx, client, token, repo, tag)
	if err != nil {
		exitf("fetch release: %v", err)
	}

	sourceManifest, err := fetchSourceManifest(ctx, client, token, repo, tag)
	if err != nil {
		exitf("fetch source manifest: %v", err)
	}

	pkg, err := catalog.BuildPackageFromRelease(repo, sourceManifest, release)
	if err != nil {
		exitf("build catalog package: %v", err)
	}

	index, err := loadIndex(manifestPath)
	if err != nil {
		exitf("load catalog: %v", err)
	}
	index = catalog.UpsertPackage(index, pkg)
	if err := writeIndex(manifestPath, index); err != nil {
		exitf("write catalog: %v", err)
	}
}

func fetchRelease(ctx context.Context, client *http.Client, token, repo, tag string) (catalog.Release, error) {
	var release catalog.Release
	if err := githubJSON(ctx, client, token, "https://api.github.com/repos/"+repo+"/releases/tags/"+tag, &release); err != nil {
		return catalog.Release{}, err
	}
	return release, nil
}

func fetchSourceManifest(ctx context.Context, client *http.Client, token, repo, tag string) (catalog.SourceManifest, error) {
	var manifest catalog.SourceManifest
	url := "https://raw.githubusercontent.com/" + repo + "/" + tag + "/manifest.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return catalog.SourceManifest{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "continuum-plugins-catalog-updater")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return catalog.SourceManifest{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return catalog.SourceManifest{}, fmt.Errorf("GET %s: status %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return catalog.SourceManifest{}, fmt.Errorf("decode source manifest: %w", err)
	}
	return manifest, nil
}

func githubJSON(ctx context.Context, client *http.Client, token, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "continuum-plugins-catalog-updater")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("GET %s: status %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func loadIndex(path string) (catalog.RepositoryIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return catalog.RepositoryIndex{}, fmt.Errorf("read %s: %w", path, err)
	}
	var index catalog.RepositoryIndex
	if len(bytes.TrimSpace(data)) == 0 {
		return index, nil
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return catalog.RepositoryIndex{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return index, nil
}

func writeIndex(path string, index catalog.RepositoryIndex) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}