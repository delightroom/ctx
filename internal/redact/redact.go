package redact

import "regexp"

type rule struct {
	pattern     *regexp.Regexp
	replacement string
}

var rules = []rule{
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED_AWS_ACCESS_KEY]"},
	{regexp.MustCompile(`\bgh[opsu]_[A-Za-z0-9_]{20,}\b`), "[REDACTED_GITHUB_TOKEN]"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), "[REDACTED_SLACK_TOKEN]"},
	{regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|password|secret)\b(\s*[:=]\s*)("[^"]+"|'[^']+'|[^\s,;]+)`), "$1$2[REDACTED]"},
}

func String(value string) string {
	for _, current := range rules {
		value = current.pattern.ReplaceAllString(value, current.replacement)
	}
	return value
}
