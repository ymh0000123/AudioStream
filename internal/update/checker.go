package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	githubAPIURL = "https://api.github.com/repos/ymh0000123/AudioStream/releases/latest"
	timeout      = 5 * time.Second
)

type ReleaseInfo struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

type CheckResult struct {
	HasUpdate   bool
	Current     string
	Latest      string
	DownloadURL string
	ReleaseURL  string
	Notes       string
}

var versionRegex = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

func parseVersion(v string) (int, int, int, bool) {
	matches := versionRegex.FindStringSubmatch(v)
	if matches == nil {
		return 0, 0, 0, false
	}
	var major, minor, patch int
	fmt.Sscanf(matches[1], "%d", &major)
	fmt.Sscanf(matches[2], "%d", &minor)
	fmt.Sscanf(matches[3], "%d", &patch)
	return major, minor, patch, true
}

func compareVersions(a, b string) int {
	aMaj, aMin, aPat, aOk := parseVersion(a)
	bMaj, bMin, bPat, bOk := parseVersion(b)
	if !aOk || !bOk {
		return 0
	}
	if aMaj != bMaj {
		if aMaj > bMaj {
			return 1
		}
		return -1
	}
	if aMin != bMin {
		if aMin > bMin {
			return 1
		}
		return -1
	}
	if aPat != bPat {
		if aPat > bPat {
			return 1
		}
		return -1
	}
	return 0
}

// CheckForUpdate 异步检查 GitHub Releases 是否有新版本
func CheckForUpdate(currentVersion string, resultChan chan<- *CheckResult) {
	defer close(resultChan)

	if currentVersion == "dev" || strings.HasSuffix(currentVersion, "-dev") {
		return
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(githubAPIURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return
	}
	if resp.StatusCode != http.StatusOK {
		return
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	if compareVersions(release.TagName, currentVersion) <= 0 {
		return
	}

	downloadURL := fmt.Sprintf("https://github.com/ymh0000123/AudioStream/releases/download/%s/server.exe", release.TagName)

	resultChan <- &CheckResult{
		HasUpdate:   true,
		Current:     currentVersion,
		Latest:      release.TagName,
		DownloadURL: downloadURL,
		ReleaseURL:  release.HTMLURL,
		Notes:       release.Body,
	}
}
