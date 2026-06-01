package cli

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

const secretKeyNamePattern = `["']?(?:[A-Z0-9_]*(?:API[_-]?KEY|PRIVATE[_-]?KEY|SECRET|PASSWORD)[A-Z0-9_-]*|(?:[A-Z0-9]+[_-])?(?:ACCESS[_-]|REFRESH[_-]|ID[_-]|AUTH[_-]|BEARER[_-])?TOKEN|(?:[A-Z0-9]+[_-])?CREDENTIALS?)["']?`

var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(` + secretKeyNamePattern + `\s*[:=]\s*)"[^"]*"`),
	regexp.MustCompile(`(?i)(` + secretKeyNamePattern + `\s*[:=]\s*)'[^']*'`),
	regexp.MustCompile(`(?i)(` + secretKeyNamePattern + `\s*[:=]\s*)[^\n#,]+`),
	regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*bearer\s+)[^\n#]+`),
	regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,})\b`),
	regexp.MustCompile(`\b(gh[opusr]_[A-Za-z0-9_]{16,})\b`),
	regexp.MustCompile(`\b(github_pat_[A-Za-z0-9_]{16,})\b`),
}

var pemPrivateKeyBlockPattern = regexp.MustCompile(`(?is)(` + secretKeyNamePattern + `\s*[:=]\s*)?-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY-{5}.*?-{5}END [A-Z0-9 ]*PRIVATE KEY-{5}`)

var secretAssignmentSeparator = regexp.MustCompile(`[:=]`)

var bareSecretPrefixes = []string{"sk-", "github_pat_", "gho_", "ghp_", "ghs_", "ghu_", "ghr_"}

func redactSecretText(value string) string {
	out := pemPrivateKeyBlockPattern.ReplaceAllStringFunc(value, func(match string) string {
		begin := strings.Index(match, "-----BEGIN")
		if loc := secretAssignmentSeparator.FindStringIndex(match); loc != nil && (begin < 0 || loc[0] < begin) {
			prefix := strings.TrimRight(match[:loc[1]], " \t")
			if strings.HasSuffix(match[:loc[1]], " ") || strings.HasSuffix(match[:loc[1]], "\t") {
				prefix += " "
			}
			return prefix + "[REDACTED_SECRET]"
		}
		return "[REDACTED_SECRET]"
	})
	for _, pattern := range secretValuePatterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			if hasAnyPrefix(match, bareSecretPrefixes) {
				return "[REDACTED_SECRET]"
			}
			prefix := match
			if loc := secretAssignmentSeparator.FindStringIndex(match); loc != nil {
				prefix = strings.TrimRight(match[:loc[1]], " \t")
				if strings.HasSuffix(match[:loc[1]], " ") || strings.HasSuffix(match[:loc[1]], "\t") {
					prefix += " "
				}
			}
			return prefix + "[REDACTED_SECRET]"
		})
	}
	return out
}

func redactPromptMetadata(value string) string {
	raw := strings.TrimSpace(value)
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return redactRepoRemote(raw)
	}
	clean := redactSecretText(raw)
	if parsed, err := url.Parse(clean); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return redactRepoRemote(clean)
	}
	if strings.Contains(clean, "@") && strings.Contains(clean, ":") {
		return redactRepoRemote(clean)
	}
	return clean
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isSensitivePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if isSensitiveName(part) {
			return true
		}
	}
	return false
}

func isSensitiveName(name string) bool {
	clean := strings.ToLower(strings.TrimSpace(name))
	switch clean {
	case "", ".", "..":
		return false
	case ".env", ".env.local", ".envrc", ".netrc", ".npmrc", ".pypirc",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"credentials", "credentials.json", "secrets", "secrets.json":
		return true
	}
	return strings.HasPrefix(clean, ".env.") ||
		strings.HasPrefix(clean, "secret.") ||
		strings.HasPrefix(clean, "secrets.") ||
		strings.Contains(clean, "credentials.") ||
		strings.HasSuffix(clean, ".pem") ||
		strings.HasSuffix(clean, ".key")
}

func redactRepoRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		host := parsed.Hostname()
		if port := parsed.Port(); port != "" {
			host += ":" + port
		}
		return parsed.Scheme + "://" + host + "/[redacted]"
	}
	if at := strings.LastIndex(remote, "@"); at >= 0 {
		remote = remote[at+1:]
	}
	if colon := strings.Index(remote, ":"); colon >= 0 {
		return remote[:colon] + ":[redacted]"
	}
	if slash := strings.Index(remote, "/"); slash >= 0 {
		return remote[:slash] + "/[redacted]"
	}
	return "[redacted]"
}

func writePrivacyAudit(command string, fields map[string]string) {
	log := openLogger(false)
	defer log.close()
	log.log("info", command, fields)
}
