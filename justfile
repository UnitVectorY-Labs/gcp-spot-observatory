
# Commands for gcp-spot-observatory
default:
  @just --list
# Build gcp-spot-observatory with Go
build:
  go build ./...

# Run tests for gcp-spot-observatory with Go
test:
  go clean -testcache
  go test ./...