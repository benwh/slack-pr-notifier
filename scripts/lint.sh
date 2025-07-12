#!/bin/bash

set -euo pipefail

echo "🔍 Running linters..."

# Go linting
echo "📝 Running Go linters..."
if command -v golangci-lint &> /dev/null; then
    golangci-lint run ./...
else
    echo "⚠️  golangci-lint not found, using basic go tools"
    go fmt ./...
    go vet ./...
    if command -v staticcheck &> /dev/null; then
        staticcheck ./...
    else
        echo "⚠️  staticcheck not found, install with: go install honnef.co/go/tools/cmd/staticcheck@latest"
    fi
fi

# Docker linting
echo "🐳 Running Docker linters..."
if command -v hadolint &> /dev/null; then
    hadolint Dockerfile
else
    echo "⚠️  hadolint not found, install with: brew install hadolint"
fi

# Shell script linting
echo "🐚 Running shell script linters..."
if command -v shellcheck &> /dev/null; then
    find scripts -name "*.sh" -exec shellcheck {} \;
else
    echo "⚠️  shellcheck not found, install with: brew install shellcheck"
fi

# Markdown linting
echo "📄 Running Markdown linters..."
if command -v markdownlint &> /dev/null; then
    markdownlint README.md docs/
else
    echo "⚠️  markdownlint not found, install with: npm install -g markdownlint-cli"
fi

# YAML linting
echo "📋 Running YAML linters..."
if command -v yamllint &> /dev/null; then
    find . \( -name "*.yaml" -o -name "*.yml" \) -exec yamllint {} \;
else
    echo "⚠️  yamllint not found, install with: pip install yamllint"
fi

echo "✅ Linting complete!"
