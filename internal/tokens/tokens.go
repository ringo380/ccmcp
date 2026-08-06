// Package tokens holds the single shared BPE encoder ccmcp uses to estimate
// token counts.
//
// Anthropic doesn't publish its tokenizer, so OpenAI's cl100k_base is the
// standard close-enough analog (within ~5% on prose, much closer than the
// 4-chars-per-token rule of thumb). Every count derived from it is an estimate
// and must be presented as one.
package tokens

import (
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

var (
	encOnce sync.Once
	enc     *tiktoken.Tiktoken
	encErr  error
)

// Encoder returns the process-wide memoised cl100k_base encoder.
func Encoder() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		enc, encErr = tiktoken.GetEncoding("cl100k_base")
	})
	return enc, encErr
}

// Count returns the BPE token count of s. An empty string is 0 tokens and
// never an error.
func Count(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	e, err := Encoder()
	if err != nil {
		return 0, err
	}
	return len(e.Encode(s, nil, nil)), nil
}

// CountLines sums Count over ss. Counting each line separately (rather than
// concatenating) keeps per-item attribution exact - a joined string can merge
// tokens across a boundary and make the parts not sum to the whole.
func CountLines(ss []string) (int, error) {
	total := 0
	for _, s := range ss {
		n, err := Count(s)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
