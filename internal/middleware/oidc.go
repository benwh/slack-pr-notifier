package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/idtoken"
)

var (
	// ErrMissingAuthHeader indicates the Authorization header is missing.
	ErrMissingAuthHeader = fmt.Errorf("missing Authorization header")
	// ErrInvalidTokenFormat indicates the token format is invalid.
	ErrInvalidTokenFormat = fmt.Errorf("invalid token format")
	// ErrTokenValidationFailed indicates token validation failed.
	ErrTokenValidationFailed = fmt.Errorf("token validation failed")
	// ErrInvalidServiceAccount indicates invalid service account in token.
	ErrInvalidServiceAccount = fmt.Errorf("invalid service account in token")
)

// OIDCMiddleware creates middleware that verifies Google Cloud OIDC tokens from Cloud Tasks.
func OIDCMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip OIDC verification if no service account email is configured (development mode)
		if cfg.CloudTasksServiceAccountEmail == "" {
			log.Debug(c.Request.Context(), "Skipping OIDC verification - no service account configured")
			c.Next()
			return
		}

		ctx := c.Request.Context()

		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			log.Error(ctx, "Missing Authorization header for Cloud Tasks request")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Check for Bearer token format
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			log.Error(ctx, "Invalid Authorization header format", "format", "expected Bearer token")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token format"})
			c.Abort()
			return
		}

		token := strings.TrimPrefix(authHeader, bearerPrefix)

		// Verify the OIDC token
		if err := verifyOIDCToken(ctx, token, cfg); err != nil {
			log.Error(ctx, "OIDC token verification failed", "error", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "token verification failed"})
			c.Abort()
			return
		}

		log.Debug(ctx, "OIDC token verification successful")
		c.Next()
	}
}

// verifyOIDCToken verifies a Google Cloud OIDC token using Google's idtoken package.
func verifyOIDCToken(ctx context.Context, tokenString string, cfg *config.Config) error {
	// Expected audience is the job processor URL
	expectedAudience := cfg.JobProcessorURL()

	// Validate the token using Google's idtoken package
	payload, err := idtoken.Validate(ctx, tokenString, expectedAudience)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTokenValidationFailed, err)
	}

	// Extract email from the payload
	email, ok := payload.Claims["email"].(string)
	if !ok {
		return fmt.Errorf("%w: missing email claim", ErrTokenValidationFailed)
	}

	// Validate service account email
	if email != cfg.CloudTasksServiceAccountEmail {
		return fmt.Errorf("%w: got %s, expected %s", ErrInvalidServiceAccount, email, cfg.CloudTasksServiceAccountEmail)
	}

	// Verify email is verified (should be true for service accounts)
	if emailVerified, ok := payload.Claims["email_verified"].(bool); ok && !emailVerified {
		return fmt.Errorf("%w: service account email not verified", ErrTokenValidationFailed)
	}

	log.Debug(ctx, "OIDC token validation successful",
		"service_account", email,
		"audience", payload.Audience,
		"issuer", payload.Issuer,
		"expires_at", payload.Expires,
	)

	return nil
}
