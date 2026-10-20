package gorobinson_test

import (
	"fmt"
	"testing"

	"github.com/jadoint/gorobinson"
)

// BenchmarkDegenerate times a fresh degenerator over 1,000 fixed words per
// op, so a cache, where one exists, never helps. It uses exported API only,
// so the same file runs against v0.3.1 (README D-7 of
// asianfanfics/docs/gorobinson-lookup-chunking).
func BenchmarkDegenerate(b *testing.B) {
	words := make([]string, 0, 1000)
	for i := 0; i < 250; i++ {
		words = append(words,
			fmt.Sprintf("Word%dExample!!", i),
			fmt.Sprintf("word%d", i),
			fmt.Sprintf("WORD%d...", i),
			fmt.Sprintf("日本%d?", i),
		)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := gorobinson.NewStandardDegenerator()
		d.Degenerate(words)
	}
}
