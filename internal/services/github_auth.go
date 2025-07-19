package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
)

const (
	// OAuth state generation and timeout constants.
	stateIDLength     = 16
	oauthStateTimeout = 15 * time.Minute
	httpClientTimeout = 30 * time.Second

	// GitHub OAuth endpoints.
	// #nosec G101 -- Public GitHub OAuth endpoint, not credentials
	githubTokenURL = "https://github.com/login/oauth/access_token"
	githubUserURL  = "https://api.github.com/user"
)

var (
	// OAuth validation errors.
	ErrStateRequired       = fmt.Errorf("state parameter is required")
	ErrInvalidState        = fmt.Errorf("invalid or expired state")
	ErrStateExpired        = fmt.Errorf("OAuth state expired")
	ErrCodeRequired        = fmt.Errorf("authorization code is required")
	ErrAuthLinkGeneration  = fmt.Errorf("failed to generate authentication link")
	ErrNoAccessToken       = fmt.Errorf("no access token received from GitHub")
	ErrTokenExchangeFailed = fmt.Errorf("GitHub OAuth token exchange failed")
	ErrGitHubOAuthError    = fmt.Errorf("GitHub OAuth error")
	ErrGitHubAPIFailed     = fmt.Errorf("GitHub API request failed")
)

// GitHubUser represents GitHub user information from OAuth.
type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GitHubAuthService handles GitHub OAuth authentication.
type GitHubAuthService struct {
	config           *config.Config
	firestoreService *FirestoreService
	httpClient       *http.Client
}

// NewGitHubAuthService creates a new GitHub authentication service.
func NewGitHubAuthService(cfg *config.Config, firestoreService *FirestoreService) *GitHubAuthService {
	return &GitHubAuthService{
		config:           cfg,
		firestoreService: firestoreService,
		httpClient:       &http.Client{Timeout: httpClientTimeout},
	}
}

// CreateOAuthState creates a new OAuth state for CSRF protection.
func (s *GitHubAuthService) CreateOAuthState(
	ctx context.Context, slackUserID, slackTeamID, slackChannel string,
) (*models.OAuthState, error) {
	// Generate random state ID
	stateBytes := make([]byte, stateIDLength)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("failed to generate OAuth state: %w", err)
	}

	state := &models.OAuthState{
		ID:           hex.EncodeToString(stateBytes),
		SlackUserID:  slackUserID,
		SlackTeamID:  slackTeamID,
		SlackChannel: slackChannel,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(oauthStateTimeout),
	}

	if err := s.firestoreService.CreateOAuthState(ctx, state); err != nil {
		return nil, fmt.Errorf("failed to store OAuth state: %w", err)
	}

	return state, nil
}

// GetOAuthURL builds the GitHub OAuth authorization URL.
func (s *GitHubAuthService) GetOAuthURL(stateID string) string {
	baseURL := "https://github.com/login/oauth/authorize"
	params := url.Values{
		"client_id":    {s.config.GitHubClientID},
		"redirect_uri": {s.config.GitHubOAuthRedirectURL},
		"scope":        {"read:user user:email"},
		"state":        {stateID},
	}

	return fmt.Sprintf("%s?%s", baseURL, params.Encode())
}

// ValidateAndConsumeState validates OAuth state and returns associated user info.
// The state is deleted after successful validation to prevent reuse.
func (s *GitHubAuthService) ValidateAndConsumeState(ctx context.Context, stateID string) (*models.OAuthState, error) {
	if stateID == "" {
		return nil, ErrStateRequired
	}

	state, err := s.firestoreService.GetOAuthState(ctx, stateID)
	if err != nil {
		log.Warn(ctx, "Invalid or expired OAuth state", "state_id", stateID, "error", err)
		return nil, ErrInvalidState
	}

	// Check if state is expired
	if time.Now().After(state.ExpiresAt) {
		// Clean up expired state
		_ = s.firestoreService.DeleteOAuthState(ctx, stateID)
		return nil, ErrStateExpired
	}

	// Delete state to prevent reuse
	if err := s.firestoreService.DeleteOAuthState(ctx, stateID); err != nil {
		log.Warn(ctx, "Failed to delete OAuth state after validation", "state_id", stateID, "error", err)
	}

	return state, nil
}

// ExchangeCodeForUser exchanges OAuth code for GitHub user information.
func (s *GitHubAuthService) ExchangeCodeForUser(ctx context.Context, code string) (*GitHubUser, error) {
	if code == "" {
		return nil, ErrCodeRequired
	}

	// Exchange code for access token
	accessToken, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	// Fetch user information using access token
	user, err := s.fetchGitHubUser(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitHub user: %w", err)
	}

	return user, nil
}

// exchangeCodeForToken exchanges authorization code for access token.
func (s *GitHubAuthService) exchangeCodeForToken(ctx context.Context, code string) (string, error) {
	tokenURL := githubTokenURL

	data := url.Values{
		"client_id":     {s.config.GitHubClientID},
		"client_secret": {s.config.GitHubClientSecret},
		"code":          {code},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.URL.RawQuery = data.Encode()

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status %d", ErrTokenExchangeFailed, resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("%w: %s - %s", ErrGitHubOAuthError, tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", ErrNoAccessToken
	}

	return tokenResp.AccessToken, nil
}

// fetchGitHubUser fetches user information from GitHub API.
func (s *GitHubAuthService) fetchGitHubUser(ctx context.Context, accessToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "GitHub-Slack-Notifier/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrGitHubAPIFailed, resp.StatusCode)
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub user response: %w", err)
	}

	return &user, nil
}
