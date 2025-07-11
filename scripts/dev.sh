#!/bin/bash

set -e

echo "🚀 Starting local development environment..."

# Check if ngrok is installed
if ! command -v ngrok &> /dev/null; then
    echo "❌ ngrok not found. Please install it:"
    echo "   brew install ngrok"
    echo "   or download from https://ngrok.com"
    exit 1
fi

# Check if environment file exists
if [ ! -f ".env" ]; then
    echo "❌ .env file not found. Please create it:"
    echo "   cp .env.example .env"
    echo "   # Edit .env with your configuration"
    exit 1
fi

# Load environment variables
set -a
# shellcheck disable=SC1091
source .env
set +a

# Start the Go application in the background
echo "🔧 Starting Go application..."
go run main.go &
APP_PID=$!

# Wait for the application to start
sleep 3

# Check if application started successfully
if ! curl -s http://localhost:8080/health > /dev/null; then
    echo "❌ Application failed to start"
    kill $APP_PID 2>/dev/null || true
    exit 1
fi

echo "✅ Application started on port 8080"

# Start ngrok tunnel
echo "🌐 Starting ngrok tunnel..."
ngrok http 8080 --log=stdout &
NGROK_PID=$!

# Wait for ngrok to start
sleep 3

# Get the public URL
NGROK_URL=$(curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url')

if [ "$NGROK_URL" = "null" ] || [ -z "$NGROK_URL" ]; then
    echo "❌ Failed to get ngrok URL"
    kill $APP_PID $NGROK_PID 2>/dev/null || true
    exit 1
fi

echo "🎉 Development environment ready!"
echo "📍 Local URL:  http://localhost:8080"
echo "🌍 Public URL: $NGROK_URL"
echo ""
echo "🔗 Webhook URLs:"
echo "   GitHub: $NGROK_URL/webhooks/github"
echo "   Slack:  $NGROK_URL/webhooks/slack"
echo ""
echo "📊 ngrok dashboard: http://localhost:4040"
echo ""
echo "Press Ctrl+C to stop..."

# Cleanup function
cleanup() {
    echo ""
    echo "🧹 Cleaning up..."
    kill $APP_PID $NGROK_PID 2>/dev/null || true
    exit 0
}

trap cleanup SIGINT SIGTERM

# Keep the script running
wait
