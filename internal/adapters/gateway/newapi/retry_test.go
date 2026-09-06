package newapi

import "testing"

func TestParseStatusCodeRanges(t *testing.T) {
	t.Parallel()
	ranges, err := parseStatusCodeRanges("401-407, 429,500-503")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 3 || ranges[0].Start != 401 || ranges[1].Start != 429 || ranges[2].End != 503 {
		t.Fatalf("unexpected ranges: %#v", ranges)
	}
	for _, invalid := range []string{"99", "600", "500-400", "500-501-502", "abc"} {
		if _, err := parseStatusCodeRanges(invalid); err == nil {
			t.Fatalf("expected %q to fail", invalid)
		}
	}
}
