package catalog

import (
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestBuildPackageFromRelease_MinimalManifestAndAssets(t *testing.T) {
	source := &pluginv1.PluginManifest{
		PluginId:       "silo.ramindex.local-metadata",
		Version:        "0.1.0",
		SiloApiVersion: "v1",
		Capabilities: []*pluginv1.CapabilityDescriptor{
			{
				Type:        "metadata_provider.v1",
				Id:          "local-metadata",
				DisplayName: "Local Metadata: NFO Sidecars",
				Description: "Read-only same-basename NFO and local artwork metadata provider for Silo.",
			},
		},
	}

	release := Release{
		TagName: "v0.1.0",
		Assets: []Asset{
			{Name: "plugin-linux-amd64-silo-plugin-local-metadata", BrowserDownloadURL: "https://example.invalid/silo-plugin-local-metadata/plugin-linux-amd64-silo-plugin-local-metadata"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.invalid/silo-plugin-local-metadata/checksums.txt"},
			{Name: "notes.txt", BrowserDownloadURL: "https://example.invalid/silo-plugin-local-metadata/notes.txt"},
		},
	}

	pkg, err := BuildPackageFromRelease("theramindex/silo-plugin-local-metadata", source, release)
	if err != nil {
		t.Fatalf("BuildPackageFromRelease() error = %v", err)
	}

	if pkg.RepoURL != "https://github.com/theramindex/silo-plugin-local-metadata" {
		t.Fatalf("RepoURL = %q", pkg.RepoURL)
	}
	if pkg.Manifest.GetPluginId() != "silo.ramindex.local-metadata" {
		t.Fatalf("PluginID = %q", pkg.Manifest.GetPluginId())
	}
	if pkg.Manifest.GetVersion() != "0.1.0" {
		t.Fatalf("Version = %q", pkg.Manifest.GetVersion())
	}
	if got := len(pkg.Binaries); got != 1 {
		t.Fatalf("Binaries length = %d, want 1", got)
	}
	if pkg.Binaries["linux/amd64"].URL == "" {
		t.Fatal("expected linux/amd64 binary URL")
	}
}

func TestBuildPackageFromRelease_RequiresSiloAPIVersion(t *testing.T) {
	source := &pluginv1.PluginManifest{
		PluginId: "silo.ramindex.local-metadata",
		Capabilities: []*pluginv1.CapabilityDescriptor{
			{Type: "metadata_provider.v1", Id: "local-metadata"},
		},
	}
	release := Release{TagName: "v0.1.0"}

	_, err := BuildPackageFromRelease("theramindex/silo-plugin-local-metadata", source, release)
	if err == nil || !strings.Contains(err.Error(), "silo_api_version") {
		t.Fatalf("expected silo_api_version error, got %v", err)
	}
}

func TestUpsertPackage_ReplacesExistingPluginAndSorts(t *testing.T) {
	index := RepositoryIndex{
		Plugins: []CatalogPackage{
			{Manifest: &pluginv1.PluginManifest{PluginId: "silo.tvdb", Version: "1.0.0"}},
			{Manifest: &pluginv1.PluginManifest{PluginId: "silo.ramindex.local-metadata", Version: "0.0.9"}},
		},
	}

	updated := CatalogPackage{
		Manifest: &pluginv1.PluginManifest{
			PluginId: "silo.ramindex.local-metadata",
			Version:  "0.1.0",
		},
		RepoURL: "https://github.com/theramindex/silo-plugin-local-metadata",
	}

	index = UpsertPackage(index, updated)

	if len(index.Plugins) != 2 {
		t.Fatalf("Plugins length = %d, want 2", len(index.Plugins))
	}
	if index.Plugins[0].Manifest.GetPluginId() != "silo.ramindex.local-metadata" {
		t.Fatalf("Plugins[0].PluginID = %q", index.Plugins[0].Manifest.GetPluginId())
	}
	if index.Plugins[0].Manifest.GetVersion() != "0.1.0" {
		t.Fatalf("Plugins[0].Version = %q", index.Plugins[0].Manifest.GetVersion())
	}
	if index.Plugins[1].Manifest.GetPluginId() != "silo.tvdb" {
		t.Fatalf("Plugins[1].PluginID = %q", index.Plugins[1].Manifest.GetPluginId())
	}
}
