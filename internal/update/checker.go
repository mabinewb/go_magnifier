package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gomagnifier/internal/version"
)

type ReleaseInfo struct {
	TagName string
	Name    string
	HTMLURL string
}

type latestReleaseResponse struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	PreRelease bool `json:"prerelease"`
}

func FetchLatestRelease(ctx context.Context, owner string, repo string) (ReleaseInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo), nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "GoMagnifier-UpdateCheck")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ReleaseInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("unexpected status: %s", response.Status)
	}

	var payload latestReleaseResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ReleaseInfo{}, err
	}
	if payload.Draft || payload.PreRelease || strings.TrimSpace(payload.TagName) == "" || strings.TrimSpace(payload.HTMLURL) == "" {
		return ReleaseInfo{}, fmt.Errorf("no stable release available")
	}

	return ReleaseInfo{TagName: payload.TagName, Name: payload.Name, HTMLURL: payload.HTMLURL}, nil
}

func IsNewerThanCurrent(tagName string) bool {
	current, ok := version.ParseComparable(version.Display())
	if !ok {
		return false
	}
	latest, ok := version.ParseComparable(tagName)
	if !ok {
		return false
	}
	for index := 0; index < len(current); index++ {
		if latest[index] > current[index] {
			return true
		}
		if latest[index] < current[index] {
			return false
		}
	}
	return false
}