package catalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type SourceCapability struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type SourceManifest struct {
	PluginID            string             `json:"plugin_id"`
	Version             string             `json:"version"`
	ContinuumAPIVersion string             `json:"continuum_api_version"`
	Capabilities        []SourceCapability `json:"capabilities"`
	GlobalConfigSchema  []json.RawMessage  `json:"global_config_schema,omitempty"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type CatalogCapability struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type CatalogManifest struct {
	PluginID            string              `json:"plugin_id"`
	Version             string              `json:"version"`
	ContinuumAPIVersion string              `json:"continuum_api_version"`
	Capabilities        []CatalogCapability `json:"capabilities"`
}

type PlatformBinary struct {
	URL string `json:"url"`
}

type CatalogPackage struct {
	Manifest     CatalogManifest           `json:"manifest"`
	RepoURL      string                    `json:"repo_url,omitempty"`
	ChecksumsURL string                    `json:"checksums_url,omitempty"`
	Binaries     map[string]PlatformBinary `json:"binaries,omitempty"`
}

type RepositoryIndex struct {
	Plugins []CatalogPackage `json:"plugins"`
}

func BuildPackageFromRelease(repo string, source SourceManifest, release Release) (CatalogPackage, error) {
	if strings.TrimSpace(source.PluginID) == "" {
		return CatalogPackage{}, fmt.Errorf("source manifest plugin_id is required")
	}
	if strings.TrimSpace(source.ContinuumAPIVersion) == "" {
		return CatalogPackage{}, fmt.Errorf("source manifest continuum_api_version is required")
	}
	if len(source.Capabilities) == 0 {
		return CatalogPackage{}, fmt.Errorf("source manifest capabilities are required")
	}

	version := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if version == "" {
		return CatalogPackage{}, fmt.Errorf("release tag_name is required")
	}
	if source.Version != version {
		return CatalogPackage{}, fmt.Errorf("source manifest version %q does not match release tag %q", source.Version, release.TagName)
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

	capabilities := make([]CatalogCapability, 0, len(source.Capabilities))
	for _, capability := range source.Capabilities {
		if strings.TrimSpace(capability.Type) == "" || strings.TrimSpace(capability.ID) == "" {
			return CatalogPackage{}, fmt.Errorf("source manifest capability type and id are required")
		}
		capabilities = append(capabilities, CatalogCapability{
			Type:        capability.Type,
			ID:          capability.ID,
			DisplayName: capability.DisplayName,
			Description: capability.Description,
		})
	}

	return CatalogPackage{
		Manifest: CatalogManifest{
			PluginID:            source.PluginID,
			Version:             source.Version,
			ContinuumAPIVersion: source.ContinuumAPIVersion,
			Capabilities:        capabilities,
		},
		RepoURL:      "https://github.com/" + repo,
		ChecksumsURL: checksumsURL,
		Binaries:     binaries,
	}, nil
}

func UpsertPackage(index RepositoryIndex, pkg CatalogPackage) RepositoryIndex {
	filtered := make([]CatalogPackage, 0, len(index.Plugins)+1)
	for _, existing := range index.Plugins {
		if existing.Manifest.PluginID == pkg.Manifest.PluginID {
			continue
		}
		filtered = append(filtered, existing)
	}
	filtered = append(filtered, pkg)

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Manifest.PluginID < filtered[j].Manifest.PluginID
	})

	index.Plugins = filtered
	return index
}

func platformKeyFromAssetName(name string) (string, bool) {
	parts := strings.Split(name, "-")
	if len(parts) != 3 {
		return "", false
	}
	if parts[0] != "plugin" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1] + "/" + parts[2], true
}