package utils

import (
	"regexp"
	"strconv"
)

// PRLink represents a parsed GitHub pull request link with extracted components.
// It contains all the necessary information to identify and work with a specific PR.
type PRLink struct {
	URL          string // Complete GitHub PR URL (e.g., "https://github.com/owner/repo/pull/123")
	Owner        string // Repository owner/organization name
	Repo         string // Repository name
	PRNumber     int    // Pull request number
	FullRepoName string // Combined "owner/repo" format for convenience
}

// ExtractPRLinks parses GitHub pull request URLs from the given message text and returns
// a slice of PRLink structs containing the extracted information.
//
// The function uses a regex pattern to match GitHub PR URLs in the format:
// https://github.com/owner/repo/pull/number
//
// If multiple PR URLs are found in the text, it returns nil to ignore the message
// as per the application's business logic to avoid ambiguous notifications.
// If a single PR URL is found, it returns a slice with one PRLink element.
// If no PR URLs are found, it returns an empty slice.
func ExtractPRLinks(text string) []PRLink {
	pattern := regexp.MustCompile(`https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)`)
	matches := pattern.FindAllStringSubmatch(text, -1)

	// Ignore messages containing multiple PR URLs
	if len(matches) > 1 {
		return nil
	}

	links := make([]PRLink, 0, len(matches))
	for _, match := range matches {
		prNumber, _ := strconv.Atoi(match[3])
		links = append(links, PRLink{
			URL:          match[0],
			Owner:        match[1],
			Repo:         match[2],
			PRNumber:     prNumber,
			FullRepoName: match[1] + "/" + match[2],
		})
	}
	return links
}
