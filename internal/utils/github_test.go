package utils

import (
	"testing"
)

func TestExtractPRLinks(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []PRLink
	}{
		{
			name: "single valid PR link",
			text: "Check out this PR: https://github.com/owner/repo/pull/123",
			expected: []PRLink{
				{
					URL:          "https://github.com/owner/repo/pull/123",
					Owner:        "owner",
					Repo:         "repo",
					PRNumber:     123,
					FullRepoName: "owner/repo",
				},
			},
		},
		{
			name:     "multiple PR links - should ignore",
			text:     "https://github.com/owner/repo/pull/123 and https://github.com/other/repo/pull/456",
			expected: nil,
		},
		{
			name:     "no PR links",
			text:     "Just some regular text with no GitHub links",
			expected: []PRLink{},
		},
		{
			name:     "GitHub link but not PR",
			text:     "Here's the repo: https://github.com/owner/repo",
			expected: []PRLink{},
		},
		{
			name:     "empty text",
			text:     "",
			expected: []PRLink{},
		},
		{
			name: "PR link with other text",
			text: "Hey team, please review https://github.com/acme/awesome-app/pull/42 when you get a chance!",
			expected: []PRLink{
				{
					URL:          "https://github.com/acme/awesome-app/pull/42",
					Owner:        "acme",
					Repo:         "awesome-app",
					PRNumber:     42,
					FullRepoName: "acme/awesome-app",
				},
			},
		},
		{
			name: "PR link with hyphens and underscores in repo name",
			text: "https://github.com/my-org/my_awesome-repo/pull/999",
			expected: []PRLink{
				{
					URL:          "https://github.com/my-org/my_awesome-repo/pull/999",
					Owner:        "my-org",
					Repo:         "my_awesome-repo",
					PRNumber:     999,
					FullRepoName: "my-org/my_awesome-repo",
				},
			},
		},
		{
			name:     "malformed PR URL",
			text:     "https://github.com/owner/repo/pull/not-a-number",
			expected: []PRLink{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPRLinks(tt.text)

			if len(result) != len(tt.expected) {
				t.Errorf("ExtractPRLinks() returned %d links, expected %d", len(result), len(tt.expected))
				return
			}

			for i, link := range result {
				expected := tt.expected[i]
				if link.URL != expected.URL {
					t.Errorf("URL mismatch: got %s, expected %s", link.URL, expected.URL)
				}
				if link.Owner != expected.Owner {
					t.Errorf("Owner mismatch: got %s, expected %s", link.Owner, expected.Owner)
				}
				if link.Repo != expected.Repo {
					t.Errorf("Repo mismatch: got %s, expected %s", link.Repo, expected.Repo)
				}
				if link.PRNumber != expected.PRNumber {
					t.Errorf("PRNumber mismatch: got %d, expected %d", link.PRNumber, expected.PRNumber)
				}
				if link.FullRepoName != expected.FullRepoName {
					t.Errorf("FullRepoName mismatch: got %s, expected %s", link.FullRepoName, expected.FullRepoName)
				}
			}
		})
	}
}
