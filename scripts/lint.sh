#!/bin/bash

set -e

echo "🔍 Running linters..."

# Go linting
echo "📝 Running Go linters..."
if command -v golangci-lint &> /dev/null; then
    golangci-lint run --enable-all --disable=gochecknoglobals,gochecknoinits,depguard,interfacer,maligned,scopelint,varcheck,deadcode,structcheck,nosnakecase,unused,ineffassign,revive,gosimple,goconst,gocyclo,gocognit,dupl,lll,funlen,cyclop,unparam,nlreturn,wsl,thelper,varnamelen,tagliatelle,exhaustive,forcetypeassert,errname,ireturn,contextcheck,containedctx,maintidx,gosmopolitan,asasalint,nilnil,exportloopref,exhaustruct,testpackage,paralleltest,tparallel,prealloc,predeclared,noctx,wrapcheck,nestif,goerr113,forbidigo,ifshort,gomoddirectives,grouper,decorder,gofmt,gci,unconvert,misspell,nolintlint,whitespace,goheader,godot,testifylint,mirror,usestdlibvars,loggercheck,dogsled,dupword,reassign,wastedassign,protogetter,perfsprint,sloglint,goconst,errorlint,ginkgolinter,spancheck,testableexamples,musttag,bodyclose,rowserrcheck,sqlclosecheck,durationcheck,nilerr,gocheckcompilerdirectives,gocritic ./...
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
    find . -name "*.yaml" -o -name "*.yml" -exec yamllint {} \;
else
    echo "⚠️  yamllint not found, install with: pip install yamllint"
fi

echo "✅ Linting complete!"