package updater

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	RepoOwner       = "esmail-mkh"
	RepoName        = "PhotoSlicer-Go"
	GitHubLatestURL = "https://api.github.com/repos/esmail-mkh/PhotoSlicer-Go/releases/latest"
	GitHubReleases  = "https://github.com/esmail-mkh/PhotoSlicer-Go/releases"
)

// UpdateInfo holds the result of an update check.
type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	ReleaseName    string `json:"release_name"`
	PublishedAt    string `json:"published_at"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

// IsInternetConnected tests whether network connectivity is active with a short timeout.
func IsInternetConnected() bool {
	// Fast probe endpoints (Cloudflare and Google DNS)
	endpoints := []string{"1.1.1.1:53", "8.8.8.8:53"}
	for _, ep := range endpoints {
		conn, err := net.DialTimeout("tcp", ep, 1200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	// Fallback DNS lookup for GitHub
	ctxConn, err := net.DialTimeout("tcp", "github.com:443", 1500*time.Millisecond)
	if err == nil {
		_ = ctxConn.Close()
		return true
	}
	_, err = net.LookupHost("api.github.com")
	return err == nil
}

// ParseVersion extracts integer version numbers from a version string (e.g. "v5.3.0", "5.3").
// It ignores leading 'v'/'V', removes build or prerelease suffixes, and pads to at least 3 parts.
func ParseVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	// Cut off pre-release or build suffixes (-rc1, +build)
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		num, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		nums = append(nums, num)
	}

	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	return nums
}

// IsNewerVersion returns true if latest is strictly newer than current.
func IsNewerVersion(latest, current string) bool {
	latestNums := ParseVersion(latest)
	currentNums := ParseVersion(current)

	maxLen := len(latestNums)
	if len(currentNums) > maxLen {
		maxLen = len(currentNums)
	}
	for len(latestNums) < maxLen {
		latestNums = append(latestNums, 0)
	}
	for len(currentNums) < maxLen {
		currentNums = append(currentNums, 0)
	}

	for i := 0; i < maxLen; i++ {
		if latestNums[i] > currentNums[i] {
			return true
		}
		if latestNums[i] < currentNums[i] {
			return false
		}
	}
	return false
}

// CheckForUpdate checks GitHub releases for a newer version than currentVersion.
// If the user has no internet connection, it returns Available: false without error.
func CheckForUpdate(currentVersion string) (*UpdateInfo, error) {
	info := &UpdateInfo{
		Available:      false,
		CurrentVersion: currentVersion,
		ReleaseURL:     GitHubReleases,
	}

	// 1. Connectivity check: If offline, exit immediately and silently
	if !IsInternetConnected() {
		return info, nil
	}

	// 2. Fetch latest release from GitHub API with 4-second timeout
	client := &http.Client{
		Timeout: 4 * time.Second,
	}

	req, err := http.NewRequest("GET", GitHubLatestURL, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("PhotoSlicer-Go/%s", currentVersion))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		// Network timeout or unreachable; exit cleanly
		return info, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("github api status: %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return info, err
	}

	// Ignore draft releases
	if release.Draft {
		return info, nil
	}

	info.LatestVersion = release.TagName
	info.ReleaseName = release.Name
	if release.HTMLURL != "" {
		info.ReleaseURL = release.HTMLURL
	}
	info.PublishedAt = release.PublishedAt

	// Check if latest release is newer
	if IsNewerVersion(release.TagName, currentVersion) {
		info.Available = true
	}

	return info, nil
}
