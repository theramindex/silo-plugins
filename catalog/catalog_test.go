package catalog

import (
	"strings"
	"testing"
)

func TestBuildPackageFromRelease_MinimalManifestAndAssets(t *testing.T) {
	source := SourceManifest{
		PluginID:            "continuum.tmdb",
		Version:             "1.2.3",
		ContinuumAPIVersion: "v1",
		Capabilities: []SourceCapability{
			{
				Type:        "metadata_provider.v1",
				ID:          "tmdb",
				DisplayName: "TMDB",
				Description: "TMDB metadata provider for Continuum.",
			},
		},
	}

	release := Release{
		TagName: "v1.2.3",
		Assets: []Asset{
			{Name: "plugin-linux-amd64", BrowserDownloadURL: "https://example.invalid/tmdb/plugin-linux-amd64"},
			{Name: "plugin-linux-arm64", BrowserDownloadURL: "https://example.invalid/tmdb/plugin-linux-arm64"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.invalid/tmdb/checksums.txt"},
			{Name: "notes.txt", BrowserDownloadURL: "https://example.invalid/tmdb/notes.txt"},
		},
	}

	pkg, err := BuildPackageFromRelease("ContinuumApp/continuum-plugin-tmdb", source, release)
	if err != nil {
		t.Fatalf("BuildPackageFromRelease() error = %v", err)
	}

	if pkg.RepoURL != "https://github.com/ContinuumApp/continuum-plugin-tmdb" {
		t.Fatalf("RepoURL = %q", pkg.RepoURL)
	}
	if pkg.Manifest.PluginID != "continuum.tmdb" {
		t.Fatalf("PluginID = %q", pkg.Manifest.PluginID)
	}
	if pkg.Manifest.Version != "1.2.3" {
		t.Fatalf("Version = %q", pkg.Manifest.Version)
	}
	if got := len(pkg.Binaries); got != 2 {
		t.Fatalf("Binaries length = %d, want 2", got)
	}
	if pkg.Binaries["linux/amd64"].URL == "" {
		t.Fatal("expected linux/amd64 binary URL")
	}
}

func TestBuildPackageFromRelease_RequiresMatchingVersion(t *testing.T) {
	source := SourceManifest{
		PluginID:            "continuum.tmdb",
		Version:             "1.2.2",
		ContinuumAPIVersion: "v1",
		Capabilities: []SourceCapability{
			{Type: "metadata_provider.v1", ID: "tmdb"},
		},
	}
	release := Release{TagName: "v1.2.3"}

	_, err := BuildPackageFromRelease("ContinuumApp/continuum-plugin-tmdb", source, release)
	if err == nil || !strings.Contains(err.Error(), "does not match release tag") {
		t.Fatalf("expected version mismatch error, got %v", err)
	}
}

func TestUpsertPackage_ReplacesExistingPluginAndSorts(t *testing.T) {
	index := RepositoryIndex{
		Plugins: []CatalogPackage{
			{
				Manifest: CatalogManifest{
					PluginID:            "continuum.tvdb",
					Version:             "1.0.0",
					ContinuumAPIVersion: "v1",
				},
			},
			{
				Manifest: CatalogManifest{
					PluginID:            "continuum.tmdb",
					Version:             "1.0.0",
					ContinuumAPIVersion: "v1",
				},
			},
		},
	}

	updated := CatalogPackage{
		Manifest: CatalogManifest{
			PluginID:            "continuum.tmdb",
			Version:             "1.2.3",
			ContinuumAPIVersion: "v1",
		},
		RepoURL: "https://github.com/ContinuumApp/continuum-plugin-tmdb",
	}

	index = UpsertPackage(index, updated)

	if len(index.Plugins) != 2 {
		t.Fatalf("Plugins length = %d, want 2", len(index.Plugins))
	}
	if index.Plugins[0].Manifest.PluginID != "continuum.tmdb" {
		t.Fatalf("Plugins[0].PluginID = %q", index.Plugins[0].Manifest.PluginID)
	}
	if index.Plugins[0].Manifest.Version != "1.2.3" {
		t.Fatalf("Plugins[0].Version = %q", index.Plugins[0].Manifest.Version)
	}
	if index.Plugins[1].Manifest.PluginID != "continuum.tvdb" {
		t.Fatalf("Plugins[1].PluginID = %q", index.Plugins[1].Manifest.PluginID)
	}
}