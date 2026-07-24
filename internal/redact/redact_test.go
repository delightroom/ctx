package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsKnownSecretShapes(t *testing.T) {
	input := strings.Join([]string{
		"aws=AKIAIOSFODNN7EXAMPLE",
		"github=ghp_abcdefghijklmnopqrstuvwxyz123456",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz.1234",
		"api_key=super-secret-value",
	}, "\n")

	output := String(input)
	for _, secret := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"abcdefghijklmnopqrstuvwxyz.1234",
		"super-secret-value",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("output contains secret %q: %s", secret, output)
		}
	}
}
