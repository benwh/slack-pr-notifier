#!/usr/bin/env bash

# Generate Slack App Manifest from template
# Reads the base URL from specified .env file
# Usage: ./scripts/generate-slack-manifest.sh <env-file>
# Example: ./scripts/generate-slack-manifest.sh production.env

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE_FILE="$PROJECT_ROOT/slack-app-manifest.template.yaml"
OUTPUT_FILE="$PROJECT_ROOT/slack-app-manifest.yaml"

# Check if env file argument is provided
if [ $# -eq 0 ]; then
    echo "❌ Error: No environment file specified"
    echo ""
    echo "Usage: $0 <env-file>"
    echo "Example: $0 production.env"
    echo "         $0 .env"
    exit 1
fi

ENV_FILE="$PROJECT_ROOT/$1"

# Check if specified .env file exists
if [ ! -f "$ENV_FILE" ]; then
    echo "❌ Error: Environment file not found at $ENV_FILE"
    echo ""
    echo "Please create the environment file with BASE_URL configured."
    echo "Example:"
    echo "  BASE_URL=https://my-service-abc123.run.app"
    exit 1
fi

# Source .env file to load environment variables
set -a  # automatically export all variables
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

# Check if BASE_URL is set
if [ -z "${BASE_URL:-}" ]; then
    echo "❌ Error: BASE_URL not found in .env file"
    echo ""
    echo "Please add BASE_URL to your .env file."
    echo "Example:"
    echo "  BASE_URL=https://my-service-abc123.run.app"
    exit 1
fi

# Set default Slack app name if not provided
if [ -z "${SLACK_APP_NAME:-}" ]; then
    SLACK_APP_NAME="PR Bot"
fi

# Remove trailing slash if present
BASE_URL="${BASE_URL%/}"

# Validate URL format
if [[ ! "$BASE_URL" =~ ^https?:// ]]; then
    echo "❌ Error: Invalid BASE_URL format"
    echo "Expected: https://domain.com"
    echo "Found: $BASE_URL"
    exit 1
fi

# Check if template file exists
if [ ! -f "$TEMPLATE_FILE" ]; then
    echo "❌ Error: Template file not found: $TEMPLATE_FILE"
    exit 1
fi

echo "🔧 Generating Slack app manifest..."
echo "📁 Template: $TEMPLATE_FILE"
echo "📄 Environment: $1"
echo "🌐 Base URL: $BASE_URL"
echo "🏷️ App Name: $SLACK_APP_NAME"
echo "📄 Output: $OUTPUT_FILE"

# Generate manifest by replacing placeholders
sed -e "s|{{BASE_URL}}|$BASE_URL|g" -e "s|{{SLACK_APP_NAME}}|$SLACK_APP_NAME|g" "$TEMPLATE_FILE" > "$OUTPUT_FILE"

echo "✅ Manifest generated successfully!"
echo ""
echo "📋 Next steps:"
echo "1. Copy the contents of $OUTPUT_FILE"
echo "2. Go to https://api.slack.com/apps"
echo "3. Click 'Create New App' → 'From an app manifest'"
echo "4. Paste the manifest content and create your app"
echo "5. Install the app to your workspace"
echo ""
echo "🔗 URLs configured:"
echo "   • Event subscriptions: $BASE_URL/webhooks/slack/events"
echo "   • Interactive components: $BASE_URL/webhooks/slack/interactions"
