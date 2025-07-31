#!/usr/bin/env bash

set -euo pipefail

echo "🔍 Running linters..."

# Go linting
echo "📝 Running Go linters..."
if command -v golangci-lint &> /dev/null; then
    golangci-lint run ./...
else
    echo "⚠️  golangci-lint not found, please install"
    exit 2
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

# Go module maintenance
echo "📦 Checking Go modules..."
go mod tidy
if ! git diff --quiet go.mod go.sum; then
    echo "❌ go.mod/go.sum files have changes after running 'go mod tidy'"
    echo "   Please commit these changes:"
    git --no-pager diff go.mod go.sum
    exit 1
fi

echo "✅ Linting complete!"
