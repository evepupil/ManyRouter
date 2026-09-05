package evaluation

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeAnswer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cell CellID
		raw  string
		want string
	}{
		{name: "ascii number", cell: CellNumber100EN, raw: "42.", want: "42"},
		{name: "full width number", cell: CellNumber100ZH, raw: "４２", want: "42"},
		{name: "english number words", cell: CellNumber100EN, raw: "forty-two", want: "42"},
		{name: "chinese number", cell: CellNumber100ZH, raw: "四十二。", want: "42"},
		{name: "first nonempty line", cell: CellNumber10EN, raw: "\n  7\nexplanation", want: "7"},
		{name: "english color alias", cell: CellColorEN, raw: "“Grey”", want: "gray"},
		{name: "multiword color", cell: CellColorEN, raw: "light-blue", want: "light_blue"},
		{name: "chinese color", cell: CellColorZH, raw: "蓝色", want: "blue"},
		{name: "english coin", cell: CellCoinEN, raw: "Heads!", want: "heads"},
		{name: "chinese coin", cell: CellCoinZH, raw: "反面", want: "tails"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeAnswer(test.cell, test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeAnswerRejectsInvalidValuesWithoutEchoingThem(t *testing.T) {
	t.Parallel()
	secret := "credential-value-that-must-not-appear"
	_, err := NormalizeAnswer(CellNumber10EN, secret)
	if !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("got %v, want invalid answer", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("normalization error exposed the answer")
	}
	if _, err := NormalizeAnswer(CellNumber10EN, "11"); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("out-of-range number was accepted: %v", err)
	}
	if _, err := NormalizeAnswer(CellID("unknown"), "42"); !errors.Is(err, ErrUnknownCell) {
		t.Fatalf("got %v, want unknown cell", err)
	}
}

func TestBuildDistributionCountsValidAndInvalidSamples(t *testing.T) {
	t.Parallel()
	distribution, err := BuildDistribution(CellCoinEN, []string{"heads", "Head", "tails", "explanation follows"})
	if err != nil {
		t.Fatal(err)
	}
	if distribution.ValidSamples() != 3 || distribution.InvalidSamples != 1 {
		t.Fatalf("unexpected sample counts: valid=%d invalid=%d", distribution.ValidSamples(), distribution.InvalidSamples)
	}
	if distribution.Counts["heads"] != 2 || distribution.Counts["tails"] != 1 {
		t.Fatalf("unexpected distribution: %#v", distribution.Counts)
	}
}
