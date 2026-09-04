package value_test

import (
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/value"
)

func TestNormalizeOpenAICompatibleBaseURLRemovesVersionPrefix(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"https://upstream.example/v1":        "https://upstream.example",
		"https://upstream.example/v1/":       "https://upstream.example",
		"https://upstream.example/openai/v1": "https://upstream.example/openai",
		"https://upstream.example/v10":       "https://upstream.example/v10",
		"https://upstream.example/api":       "https://upstream.example/api",
	}
	for input, expected := range tests {
		input, expected := input, expected
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			actual, err := value.NormalizeOpenAICompatibleBaseURL(input)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("expected %q, got %q", expected, actual)
			}
		})
	}
}
