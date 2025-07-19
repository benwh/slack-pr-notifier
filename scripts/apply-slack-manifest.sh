#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Default values
MANIFEST_FILE="$PROJECT_ROOT/slack-app-manifest.yaml"
ACTION="update"

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Apply or create Slack app from manifest file.

OPTIONS:
    -h, --help              Show this help message
    -m, --manifest FILE     Path to manifest file (default: slack-app-manifest.yaml)
    -c, --create           Create new app instead of updating existing
    -a, --app-id APP_ID    Slack app ID (required for update, ignored for create)
    -t, --token TOKEN      Slack app configuration token (overrides SLACK_CONFIG_ACCESS_TOKEN)

ENVIRONMENT VARIABLES:
    SLACK_CONFIG_ACCESS_TOKEN    Slack app configuration access token (required)
    SLACK_APP_ID                Default app ID for updates (can be overridden with -a)

EXAMPLES:
    # Update existing app using environment variables
    $0

    # Create new app
    $0 --create

    # Update specific app with custom manifest
    $0 --app-id A1234567890 --manifest custom-manifest.yaml

    # Update with inline token
    $0 --token xoxe-1-xxxxx --app-id A1234567890

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -m|--manifest)
            MANIFEST_FILE="$2"
            shift 2
            ;;
        -c|--create)
            ACTION="create"
            shift
            ;;
        -a|--app-id)
            SLACK_APP_ID="$2"
            shift 2
            ;;
        -t|--token)
            SLACK_CONFIG_ACCESS_TOKEN="$2"
            shift 2
            ;;
        *)
            echo -e "${RED}Error: Unknown option $1${NC}" >&2
            usage >&2
            exit 1
            ;;
    esac
done

# Check if slack-manifest is installed
if ! command -v slack-manifest &> /dev/null; then
    echo -e "${RED}Error: slack-manifest CLI is not installed${NC}" >&2
    echo -e "${YELLOW}Install with: npm install -g slack-manifest${NC}" >&2
    exit 1
fi

# Check if manifest file exists
if [[ ! -f "$MANIFEST_FILE" ]]; then
    echo -e "${RED}Error: Manifest file not found: $MANIFEST_FILE${NC}" >&2
    exit 1
fi

# Check for required environment variables
if [[ -z "${SLACK_CONFIG_ACCESS_TOKEN:-}" ]]; then
    echo -e "${RED}Error: SLACK_CONFIG_ACCESS_TOKEN environment variable is required${NC}" >&2
    echo -e "${YELLOW}Get your token from: https://api.slack.com/apps -> Your App -> App-Level Tokens${NC}" >&2
    exit 1
fi

# Check if yq is available for YAML to JSON conversion
if ! command -v yq &> /dev/null; then
    echo -e "${RED}Error: yq is not installed${NC}" >&2
    echo -e "${YELLOW}Install with: brew install yq (macOS) or apt-get install yq (Ubuntu)${NC}" >&2
    exit 1
fi

# Convert YAML to JSON if needed
WORKING_MANIFEST="$MANIFEST_FILE"
if [[ "$MANIFEST_FILE" == *.yaml || "$MANIFEST_FILE" == *.yml ]]; then
    echo -e "${YELLOW}Converting YAML manifest to JSON for CLI compatibility${NC}"
    TEMP_JSON_MANIFEST=$(mktemp "${PROJECT_ROOT}/slack-app-manifest-XXXXXX.json")

    if ! yq eval -o=json "$MANIFEST_FILE" > "$TEMP_JSON_MANIFEST"; then
        echo -e "${RED}Error: Failed to convert YAML to JSON${NC}" >&2
        rm -f "$TEMP_JSON_MANIFEST"
        exit 1
    fi

    WORKING_MANIFEST="$TEMP_JSON_MANIFEST"
    # Cleanup function to remove temp file
    cleanup_temp_files() {
        rm -f "$TEMP_JSON_MANIFEST"
    }
    trap cleanup_temp_files EXIT
fi

# Build command arguments
COMMAND_ARGS=("-m" "$WORKING_MANIFEST" "-at" "$SLACK_CONFIG_ACCESS_TOKEN")

if [[ "$ACTION" == "create" ]]; then
    echo -e "${GREEN}Creating new Slack app with manifest: $MANIFEST_FILE${NC}"
    COMMAND_ARGS=("-c" "${COMMAND_ARGS[@]}")
else
    # Update mode - require app ID
    if [[ -z "${SLACK_APP_ID:-}" ]]; then
        echo -e "${RED}Error: SLACK_APP_ID environment variable or --app-id argument is required for updates${NC}" >&2
        echo -e "${YELLOW}Find your app ID at: https://api.slack.com/apps -> Your App -> Basic Information${NC}" >&2
        exit 1
    fi

    echo -e "${GREEN}Updating Slack app $SLACK_APP_ID with manifest: $MANIFEST_FILE${NC}"
    COMMAND_ARGS=("-u" "${COMMAND_ARGS[@]}" "-a" "$SLACK_APP_ID")
fi

# Execute the command
echo -e "${YELLOW}Running: slack-manifest ${COMMAND_ARGS[*]}${NC}"
if slack-manifest "${COMMAND_ARGS[@]}"; then
    echo -e "${GREEN}✓ Slack app manifest applied successfully${NC}"

    # Provide helpful next steps
    if [[ "$ACTION" == "create" ]]; then
        echo -e "${YELLOW}Next steps:${NC}"
        echo -e "1. Visit your new app: ${GREEN}https://api.slack.com/apps${NC}"
        echo -e "2. Install the app to your workspace to get the bot token"
    else
        echo -e "${YELLOW}Next steps:${NC}"
        echo -e "1. Re-authorize your app: ${GREEN}https://api.slack.com/apps/$SLACK_APP_ID/install-on-team${NC}"
        echo -e "2. Or visit app settings: ${GREEN}https://api.slack.com/apps/$SLACK_APP_ID${NC}"

        echo -e "${YELLOW}Manually open the re-authorization URL above to complete the process${NC}"
    fi
else
    echo -e "${RED}✗ Failed to apply Slack app manifest${NC}" >&2

    # Provide troubleshooting help
    echo -e "${YELLOW}Troubleshooting:${NC}" >&2
    echo -e "1. Verify your SLACK_CONFIG_ACCESS_TOKEN is valid and has 'apps:write' scope" >&2
    echo -e "2. Check your app ID is correct: ${GREEN}https://api.slack.com/apps${NC}" >&2
    echo -e "3. Ensure the manifest file is valid JSON/YAML" >&2

    exit 1
fi
