package detector

import (
	"path/filepath"
	"testing"

	"github.com/tsbkw/agentlens/internal/providers"
)

// The built-in auth pattern must catch the error texts real tools emit and ignore the many
// other places "401" and "403" show up in a failed call's output.
func TestBuiltinAuthRegexPrecision(t *testing.T) {
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	match := func(s string) bool {
		for _, re := range loaded.AuthFailureRegex {
			if re.MatchString(s) {
				return true
			}
		}
		return false
	}
	for _, s := range []string{
		"Authentication Failed: Bad credentials",
		"Error executing tool execute_sql: 403 POST https://api.example.com/v1/query",
		"Error refreshing your current auth tokens: Reauthentication failed. cannot prompt during non-interactive execution.",
		`MCP server "notion" requires re-authorization (token expired)`,
		"AxiosError: Request failed with status code 401",
		"HTTP 401: Bad credentials (https://api.github.com/graphql)",
		"gh: Bad credentials (HTTP 401)",
		`Dynamic Client Registration rejected (HTTP 401): {"error":"invalid session"}`,
		"rpc error: HTTP status code received from server: 403 (Forbidden)",
		"upload failed: HTTP 401 for photo.jpg",
		`{"status": "401", "message": "Unauthorized"}`,
		`<title>403</title>403 Forbidden`,
		"Invalid API key provided",
		"The access token has expired",
	} {
		if !match(s) {
			t.Errorf("Expected an auth failure match for %q", s)
		}
	}
	for _, s := range []string{
		"| 1288 | Trading cards | 29,840 | 403,040 | 85.3 |",
		"both endpoints returned 200 OK with the auth header (no more 401)",
		"syntax error at line 401",
		"403 bytes truncated",
		"Permission denied (publickey)",
		"wrote 1401 bytes to /tmp/out",
	} {
		if match(s) {
			t.Errorf("Expected no auth failure match for %q", s)
		}
	}
}
