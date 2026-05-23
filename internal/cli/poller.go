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
	// If etag is non-empty, sends If-None-Match header and returns notModified=true on 304.
	// On success (200), returns (commit, etag, false, nil).
	// On 304 (Not Modified), returns (cachedCommit, cachedETag, true, nil).
	// If gh CLI is not found or auth fails, returns ("", "", false, error).
	Poll(owner, repo, ref, etag string) (commit, newETag string, notModified bool, err error)
}

// ghPoller is the production implementation that shells out to `gh api`.
type ghPoller struct{}

func newGHPoller() GitHubPoller {
	return &ghPoller{}
}

// Poll calls gh api repos/{owner}/{repo}/commits/{ref} with ETag headers.
// If cachedETag is non-empty, sends If-None-Match header.
// Returns (commit, newETag, false, nil) on 200 OK.
// Returns (cachedCommit, cachedETag, true, nil) on 304 Not Modified.
func (p *ghPoller) Poll(owner, repo, ref, cachedETag string) (commit, newETag string, notModified bool, err error) {
	// Check if gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return "", "", false, fmt.Errorf("gh CLI not found: please install GitHub CLI or add it to PATH")
	}

	// Build command with If-None-Match header if etag is cached
	args := []string{"api", "-i", "repos/" + owner + "/" + repo + "/commits/" + ref}
	if cachedETag != "" {
		args = append(args, "-H", "If-None-Match: "+cachedETag)
	}

	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// gh returns 1 on HTTP errors; check if it's a 304
			output := stderr.String()
			if strings.Contains(output, "304") {
				// Not Modified; return cached values with notModified=true
				return "", "", true, nil
			}
		}
		return "", "", false, fmt.Errorf("gh api call failed: %w\nstderr: %s", err, stderr.String())
	}

	// Parse response: headers come first, then blank line, then JSON body
	respText := stdout.String()
	// Normalize CRLF to LF so the split works with real HTTP responses
	respText = strings.ReplaceAll(respText, "\r\n", "\n")
	parts := strings.SplitN(respText, "\n\n", 2)
	if len(parts) < 2 {
		return "", "", false, fmt.Errorf("unexpected gh response format: no body")
	}

	headers := parts[0]
	body := parts[1]

	// Extract ETag from response headers
	newETag = extractETag(headers)

	// Parse JSON body to get commit SHA
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return "", "", false, fmt.Errorf("parse gh response: %w", err)
	}

	sha, ok := result["sha"].(string)
	if !ok {
		return "", "", false, fmt.Errorf("gh response missing or invalid 'sha' field")
	}

	return sha, newETag, false, nil
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
