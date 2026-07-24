package client

import "testing"

func TestParseLocator(t *testing.T) {
	tests := []struct {
		input string
		host  string
		feed  string
	}{
		{"dev-laptop/payments", "dev-laptop", "payments"},
		{"ctx://dev-laptop/payments", "dev-laptop", "payments"},
		{"https://dev-laptop.example.ts.net:8443/payments", "https://dev-laptop.example.ts.net:8443", "payments"},
	}
	for _, test := range tests {
		locator, err := ParseLocator(test.input)
		if err != nil {
			t.Fatalf("ParseLocator(%q): %v", test.input, err)
		}
		if locator.Host != test.host || locator.Feed != test.feed {
			t.Fatalf("ParseLocator(%q) = %+v", test.input, locator)
		}
	}
}

func TestParseLocatorRejectsMissingFeed(t *testing.T) {
	if _, err := ParseLocator("dev-laptop"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseLocatorRejectsUnsupportedScheme(t *testing.T) {
	if _, err := ParseLocator("ftp://dev-laptop/payments"); err == nil {
		t.Fatal("expected an error")
	}
}
