package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// GitHubPoller defines the interface for polling GitHub for commit info.
// This allows tests to substitute a fake implementation.
type GitHubPoller interface {
	// Poll fetches the current commit SHA and ETag for the given GitHub repo and ref.
	// On success, returns (commit, etag, nil).
	// On 304 (Not Modified), returns ("", etag, nil) to indicate no change.
	// If gh CLI is not found or auth fails, returns ("", "", error).
	Poll(owner, repo, ref string) (commit, etag string, err error)
}

// ghPoller is the production implementation that shells out to `gh api`.
type ghPoller struct{}

func newGHPoller() GitHubPoller {
	return &ghPoller{}
}

// Poll calls gh api repos/{owner}/{repo}/commits/{ref} with ETag headers.
// The caller is expected to pass the cached etag (if any) via the context;
// we rely on gh CLI to handle If-None-Match logic.
// For simplicity in v0.1, we always make a fresh request and parse the response.
func (p *ghPoller) Poll(owner, repo, ref string) (commit, etag string, err error) {
	// Check if gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return "", "", fmt.Errorf("gh CLI not found: please install GitHub CLI or add it to PATH")
	}

	// Call gh api with headers output
	cmd := exec.Command("gh", "api", "-i", "repos/"+owner+"/"+repo+"/commits/"+ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// gh returns 1 on HTTP errors; check if it's a 304
			output := stderr.String()
			if strings.Contains(output, "304") {
				// Not Modified; no body, no commit to return
				return "", "", nil
			}
		}
		return "", "", fmt.Errorf("gh api call failed: %w\nstderr: %s", err, stderr.String())
	}

	// Parse response: headers come first, then blank line, then JSON body
	respText := stdout.String()
	// Normalize CRLF to LF so the split works with real HTTP responses
	respText = strings.ReplaceAll(respText, "\r\n", "\n")
	parts := strings.SplitN(respText, "\n\n", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected gh response format: no body")
	}

	headers := parts[0]
	body := parts[1]

	// Extract ETag from response headers
	etag = extractETag(headers)

	// Parse JSON body to get commit SHA
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return "", "", fmt.Errorf("parse gh response: %w", err)
	}

	sha, ok := result["sha"].(string)
	if !ok {
		return "", "", fmt.Errorf("gh response missing or invalid 'sha' field")
	}

	return sha, etag, nil
}

// extractETag pulls the ETag value from HTTP headers.
func extractETag(headers string) string {
	for _, line := range strings.Split(headers, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "etag:") {
			return strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "etag:"))
		}
	}
	return ""
}
