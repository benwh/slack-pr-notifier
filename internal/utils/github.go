package utils

import (
	"regexp"
	"strconv"
)

// PRLink represents a parsed GitHub PR link.
type PRLink struct {
	URL          string
	Owner        string
	Repo         string
	PRNumber     int
	FullRepoName string // "owner/repo"
}

// ExtractPRLinks parses GitHub PR URLs from message text.
// If multiple PR URLs are found, returns nil to ignore the message.
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
