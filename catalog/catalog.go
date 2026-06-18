package catalog

import (
	"fmt"
	"sort"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type SourceManifest = pluginv1.PluginManifest

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type PlatformBinary struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum,omitempty"`
}

type CatalogPackage struct {
	Manifest     *pluginv1.PluginManifest  `json:"manifest"`
	RepoURL      string                    `json:"repo_url,omitempty"`
	ChecksumsURL string                    `json:"checksums_url,omitempty"`
	Binaries     map[string]PlatformBinary `json:"binaries,omitempty"`
}

type RepositoryIndex struct {
	Plugins []CatalogPackage `json:"plugins"`
}

func DecodeSourceManifest(data []byte) (*SourceManifest, error) {
	var manifest pluginv1.PluginManifest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode source manifest: %w", err)
	}
	return &manifest, nil
}

func BuildPackageFromRelease(repo string, source *SourceManifest, release Release) (CatalogPackage, error) {
	if source == nil {
		return CatalogPackage{}, fmt.Errorf("source manifest is required")
	}
	if strings.TrimSpace(source.GetPluginId()) == "" {
		return CatalogPackage{}, fmt.Errorf("source manifest plugin_id is required")
	}
	if strings.TrimSpace(source.GetSiloApiVersion()) == "" {
		return CatalogPackage{}, fmt.Errorf("source manifest silo_api_version is required")
	}
	if len(source.GetCapabilities()) == 0 {
		return CatalogPackage{}, fmt.Errorf("source manifest capabilities are required")
	}

	version := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if version == "" {
		return CatalogPackage{}, fmt.Errorf("release tag_name is required")
	}

	checksumsURL := ""
	binaries := map[string]PlatformBinary{}
	for _, asset := range release.Assets {
		switch {
		case asset.Name == "checksums.txt":
			checksumsURL = asset.BrowserDownloadURL
		case strings.HasPrefix(asset.Name, "plugin-"):
			platform, ok := platformKeyFromAssetName(asset.Name)
			if !ok {
				continue
			}
			binaries[platform] = PlatformBinary{URL: asset.BrowserDownloadURL}
		}
	}

	if checksumsURL == "" {
		return CatalogPackage{}, fmt.Errorf("release %q is missing checksums.txt", release.TagName)
	}
	if len(binaries) == 0 {
		return CatalogPackage{}, fmt.Errorf("release %q has no plugin binaries", release.TagName)
	}

	for _, capability := range source.GetCapabilities() {
		if strings.TrimSpace(capability.GetType()) == "" || strings.TrimSpace(capability.GetId()) == "" {
			return CatalogPackage{}, fmt.Errorf("source manifest capability type and id are required")
		}
	}

	manifest := proto.Clone(source).(*pluginv1.PluginManifest)
	manifest.Version = version
	manifest.Checksum = ""

	return CatalogPackage{
		Manifest:     manifest,
		RepoURL:      "https://github.com/" + repo,
		ChecksumsURL: checksumsURL,
		Binaries:     binaries,
	}, nil
}

func UpsertPackage(index RepositoryIndex, pkg CatalogPackage) RepositoryIndex {
	filtered := make([]CatalogPackage, 0, len(index.Plugins)+1)
	for _, existing := range index.Plugins {
		if packagePluginID(existing) == packagePluginID(pkg) {
			continue
		}
		filtered = append(filtered, existing)
	}
	filtered = append(filtered, pkg)

	sort.Slice(filtered, func(i, j int) bool {
		return packagePluginID(filtered[i]) < packagePluginID(filtered[j])
	})

	index.Plugins = filtered
	return index
}

func packagePluginID(pkg CatalogPackage) string {
	if pkg.Manifest == nil {
		return ""
	}
	return pkg.Manifest.GetPluginId()
}

func platformKeyFromAssetName(name string) (string, bool) {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return "", false
	}
	if parts[0] != "plugin" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1] + "/" + parts[2], true
}
