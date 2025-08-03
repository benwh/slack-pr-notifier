#!/usr/bin/env bash

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
TOKEN_FILE="$PROJECT_ROOT/tmp/slack-config-access-token"

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

# Load environment variables from .env if it exists
if [[ -f "$PROJECT_ROOT/.env" ]]; then
    set -a  # automatically export all variables
    # shellcheck source=/dev/null
    source "$PROJECT_ROOT/.env"
    set +a
fi

# Token management functions
get_token_from_file() {
    if [[ -f "$TOKEN_FILE" ]]; then
        # Clean any whitespace that might have gotten into the file
        tr -d '\n\r\t ' < "$TOKEN_FILE" | sed 's/[[:space:]]//g'
    else
        echo ""
    fi
}

save_token_to_file() {
    local token="$1"
    # Clean the token before saving (remove any whitespace that might cause issues)
    token=$(echo "$token" | tr -d '\n\r\t ' | sed 's/[[:space:]]//g')
    mkdir -p "$(dirname "$TOKEN_FILE")"
    echo -n "$token" > "$TOKEN_FILE"  # Use -n to avoid adding newline
}

is_token_expired() {
    local token="$1"
    if [[ -z "$token" ]]; then
        return 0  # Empty token is considered expired
    fi

    # Extract exp claim from JWT token (tokens are in format: xoxe-1-xxxxx)
    # For simplicity, we'll try to use the token and let the API tell us if it's expired
    # A more robust solution would decode the JWT, but that requires additional dependencies
    return 1  # Assume token is valid, let API validation handle expiry
}

refresh_config_token() {
    if [[ -z "${SLACK_CONFIG_REFRESH_TOKEN:-}" ]]; then
        echo -e "${RED}Error: SLACK_CONFIG_REFRESH_TOKEN not found in .env${NC}" >&2
        return 1
    fi

    echo -e "${YELLOW}Refreshing Slack config access token...${NC}"

    # Call Slack's tooling.tokens.rotate API
    local refresh_response
    if ! refresh_response=$(curl -s -X POST "https://slack.com/api/tooling.tokens.rotate" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "refresh_token=${SLACK_CONFIG_REFRESH_TOKEN}"); then
        echo -e "${RED}Error: Failed to call token refresh API${NC}" >&2
        return 1
    fi

    # Parse the JSON response
    local ok token refresh_token
    ok=$(echo "$refresh_response" | jq -r '.ok // false')

    if [[ "$ok" != "true" ]]; then
        local error_msg
        error_msg=$(echo "$refresh_response" | jq -r '.error // "Unknown error"')
        echo -e "${RED}Error: Token refresh failed: $error_msg${NC}" >&2
        return 1
    fi

    token=$(echo "$refresh_response" | jq -r '.token // ""')
    refresh_token=$(echo "$refresh_response" | jq -r '.refresh_token // ""')

    if [[ -z "$token" ]]; then
        echo -e "${RED}Error: No token in refresh response${NC}" >&2
        return 1
    fi

    # Clean the token (remove any whitespace/newlines that might cause header issues)
    token=$(echo "$token" | tr -d '\n\r\t ' | sed 's/[[:space:]]//g')

    # Validate token format (should start with xoxe-)
    if [[ ! "$token" =~ ^xoxe- ]]; then
        echo -e "${RED}Error: Invalid token format received: ${token:0:10}...${NC}" >&2
        return 1
    fi

    # Save new tokens
    save_token_to_file "$token"
    echo -e "${GREEN}✓ Config access token refreshed and saved${NC}"

    # Update refresh token in memory (user should update .env manually)
    if [[ -n "$refresh_token" ]]; then
        refresh_token=$(echo "$refresh_token" | tr -d '\n\r\t ' | sed 's/[[:space:]]//g')
        echo -e "${YELLOW}Note: Update SLACK_CONFIG_REFRESH_TOKEN in .env with: $refresh_token${NC}"
    fi

    # Return the new token
    echo "$token"
}

get_valid_token() {
    local token

    # First try to get token from file
    token=$(get_token_from_file)

    # If no token in file, fall back to environment variable
    if [[ -z "$token" ]]; then
        token="${SLACK_CONFIG_ACCESS_TOKEN:-}"
    fi

    # If still no token, try to refresh
    if [[ -z "$token" ]]; then
        if ! token=$(refresh_config_token); then
            return 1
        fi
    fi

    echo "$token"
}

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

# Get a valid access token (from file, environment, or refresh)
SLACK_CONFIG_ACCESS_TOKEN=$(get_valid_token)
if [[ $? -ne 0 || -z "$SLACK_CONFIG_ACCESS_TOKEN" ]]; then
    echo -e "${RED}Error: Unable to obtain valid SLACK_CONFIG_ACCESS_TOKEN${NC}" >&2
    echo -e "${YELLOW}Make sure you have either:${NC}" >&2
    echo -e "1. A valid token in ./tmp/slack-config-access-token" >&2
    echo -e "2. SLACK_CONFIG_ACCESS_TOKEN in environment/command line" >&2
    echo -e "3. SLACK_CONFIG_REFRESH_TOKEN in .env for automatic refresh" >&2
    echo -e "${YELLOW}Get tokens from: https://api.slack.com/apps -> Your App -> App-Level Tokens${NC}" >&2
    exit 1
fi

# Check if jq is available for JSON parsing
if ! command -v jq &> /dev/null; then
    echo -e "${RED}Error: jq is not installed${NC}" >&2
    echo -e "${YELLOW}Install with: brew install jq (macOS) or apt-get install jq (Ubuntu)${NC}" >&2
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

# Execute the command with retry logic for token expiry
# Create a safe version of command args for logging (mask the token)
SAFE_COMMAND_ARGS=("${COMMAND_ARGS[@]}")
for i in "${!SAFE_COMMAND_ARGS[@]}"; do
    if [[ "${SAFE_COMMAND_ARGS[$i]}" == "-at" && $((i+1)) -lt ${#SAFE_COMMAND_ARGS[@]} ]]; then
        SAFE_COMMAND_ARGS[i+1]="***masked***"
        break
    fi
done
echo -e "${YELLOW}Running: slack-manifest ${SAFE_COMMAND_ARGS[*]}${NC}"

attempt_manifest_update() {
    slack-manifest "${COMMAND_ARGS[@]}"
}

if attempt_manifest_update; then
    echo -e "${GREEN}✓ Slack app manifest applied successfully${NC}"

    # Save the successful token for future use
    save_token_to_file "$SLACK_CONFIG_ACCESS_TOKEN"

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
    # Check if failure was due to token expiry and retry once with refresh
    echo -e "${YELLOW}First attempt failed, trying token refresh...${NC}"

    # Try to refresh the token and retry
    REFRESHED_TOKEN=$(refresh_config_token)
    refresh_exit_code=$?

    if [[ $refresh_exit_code -eq 0 && -n "$REFRESHED_TOKEN" ]]; then
        echo -e "${GREEN}Token refresh successful, got new token: ${REFRESHED_TOKEN:0:10}...${NC}"
        # Update the command args with the new token
        COMMAND_ARGS=("${COMMAND_ARGS[@]}")  # Copy array
        for i in "${!COMMAND_ARGS[@]}"; do
            if [[ "${COMMAND_ARGS[$i]}" == "-at" && $((i+1)) -lt ${#COMMAND_ARGS[@]} ]]; then
                COMMAND_ARGS[i+1]="$REFRESHED_TOKEN"
                break
            fi
        done

        echo -e "${YELLOW}Retrying with refreshed token...${NC}"
        if attempt_manifest_update; then
            echo -e "${GREEN}✓ Slack app manifest applied successfully after token refresh${NC}"

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
            exit 0
        fi
    else
        echo -e "${RED}Token refresh failed (exit code: $refresh_exit_code)${NC}" >&2
        if [[ -n "$REFRESHED_TOKEN" ]]; then
            echo -e "${RED}Got token but it was empty or invalid${NC}" >&2
        fi
    fi

    # Both attempts failed
    echo -e "${RED}✗ Failed to apply Slack app manifest${NC}" >&2

    # Provide troubleshooting help
    echo -e "${YELLOW}Troubleshooting:${NC}" >&2
    echo -e "1. Verify your SLACK_CONFIG_ACCESS_TOKEN/SLACK_CONFIG_REFRESH_TOKEN are valid" >&2
    echo -e "2. Check your app ID is correct: ${GREEN}https://api.slack.com/apps${NC}" >&2
    echo -e "3. Ensure the manifest file is valid JSON/YAML" >&2
    echo -e "4. Verify the config token has 'apps:write' scope" >&2

    exit 1
fi
